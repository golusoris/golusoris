// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package scim implements a minimal SCIM 2.0 server for user and group
// provisioning (RFC 7643 + RFC 7644). The package ships HTTP handlers
// mounted under `/scim/v2/`, validating one bounded SCIM JSON envelope and
// delegating CRUD to a pluggable [Store].
//
// Apps mount `scim.Handler(store)` on a router (typically chi) and
// authenticate the route with a bearer token at the middleware layer.
package scim

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/golusoris/golusoris/core/validate"
)

// Standard schema URIs (RFC 7643 §8).
const (
	SchemaUser                       = "urn:ietf:params:scim:schemas:core:2.0:User"
	SchemaGroup                      = "urn:ietf:params:scim:schemas:core:2.0:Group"
	SchemaList                       = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	SchemaError                      = "urn:ietf:params:scim:api:messages:2.0:Error"
	mediaTypeKey                     = "application/scim+json"
	defaultMaxRequestBodyBytes int64 = 1 << 20
	defaultPageSize                  = 100
	maxPageSize                      = 1000
)

var errTrailingJSON = errors.New("scim: request body contains multiple JSON values")

// User is the SCIM 2.0 User resource (subset).
type User struct {
	Schemas    []string `json:"schemas"`
	ID         string   `json:"id"`
	UserName   string   `json:"userName"`
	Active     bool     `json:"active"`
	Name       *Name    `json:"name,omitempty"`
	Emails     []Email  `json:"emails,omitempty"`
	ExternalID string   `json:"externalId,omitempty"`
}

// Name is the structured name sub-object.
type Name struct {
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
	Formatted  string `json:"formatted,omitempty"`
}

// Email is one entry in the User.emails array.
type Email struct {
	Value   string `json:"value"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

// Group is the SCIM 2.0 Group resource.
type Group struct {
	Schemas     []string `json:"schemas"`
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	Members     []Member `json:"members,omitempty"`
	ExternalID  string   `json:"externalId,omitempty"`
}

// Member is one entry in Group.members.
type Member struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
	Type    string `json:"type,omitempty"` // "User" or "Group"
}

// ListResponse wraps a list of resources with pagination metadata.
type ListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    []any    `json:"Resources"`
}

// Error is the SCIM error response.
type Error struct {
	Schemas []string `json:"schemas"`
	Status  string   `json:"status"`
	Detail  string   `json:"detail"`
	ScimErr string   `json:"scimType,omitempty"`
}

// Store is the persistence contract for SCIM resources.
type Store interface {
	CreateUser(ctx context.Context, u User) (User, error)
	GetUser(ctx context.Context, id string) (User, error)
	ListUsers(ctx context.Context, start, count int, filter string) (users []User, total int, err error)
	UpdateUser(ctx context.Context, u User) (User, error)
	DeleteUser(ctx context.Context, id string) error

	CreateGroup(ctx context.Context, g Group) (Group, error)
	GetGroup(ctx context.Context, id string) (Group, error)
	ListGroups(ctx context.Context, start, count int, filter string) (groups []Group, total int, err error)
	UpdateGroup(ctx context.Context, g Group) (Group, error)
	DeleteGroup(ctx context.Context, id string) error
}

// ErrNotFound is returned by Store implementations when the resource is
// missing. Handlers translate it to HTTP 404.
var ErrNotFound = errors.New("scim: resource not found")

// HandlerOption configures a SCIM handler.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	logger       *slog.Logger
	maxBodyBytes int64
}

// WithLogger sends internal store failures to logger without exposing them in
// SCIM responses.
func WithLogger(logger *slog.Logger) HandlerOption {
	return func(opts *handlerOptions) { opts.logger = logger }
}

// WithMaxRequestBodyBytes changes the JSON request limit. Non-positive values
// retain the 1 MiB default.
func WithMaxRequestBodyBytes(maxBytes int64) HandlerOption {
	return func(opts *handlerOptions) {
		if maxBytes > 0 {
			opts.maxBodyBytes = maxBytes
		}
	}
}

type handlerDeps struct {
	store        Store
	logger       *slog.Logger
	maxBodyBytes int64
}

// Handler returns an http.Handler implementing /Users and /Groups with the
// default logger and request-body limit. Mount it under "/scim/v2/".
func Handler(s Store) http.Handler {
	return HandlerWithOptions(s)
}

// HandlerWithOptions returns an http.Handler implementing /Users and /Groups
// with explicit handler options. Mount it under "/scim/v2/".
func HandlerWithOptions(s Store, options ...HandlerOption) http.Handler {
	opts := handlerOptions{
		logger:       slog.New(slog.DiscardHandler),
		maxBodyBytes: defaultMaxRequestBodyBytes,
	}
	for _, apply := range options {
		if apply != nil {
			apply(&opts)
		}
	}
	if opts.logger == nil {
		opts.logger = slog.New(slog.DiscardHandler)
	}
	deps := handlerDeps{store: s, logger: opts.logger, maxBodyBytes: opts.maxBodyBytes}
	if validate.IsNil(s) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deps.internalError(w, r, "route request", errors.New("missing store"))
		})
	}
	mux := http.NewServeMux()
	mux.Handle("/Users", userListHandler{deps})
	mux.Handle("/Users/", userItemHandler{deps})
	mux.Handle("/Groups", groupListHandler{deps})
	mux.Handle("/Groups/", groupItemHandler{deps})
	return mux
}

// --- Users ---

type userListHandler struct{ handlerDeps }

func (h userListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		start, count := paging(r)
		users, total, err := h.store.ListUsers(r.Context(), start, count, r.URL.Query().Get("filter"))
		if err != nil {
			h.internalError(w, r, "list users", err)
			return
		}
		users = capPage(users, count)
		out := ListResponse{
			Schemas:      []string{SchemaList},
			TotalResults: total,
			StartIndex:   start,
			ItemsPerPage: len(users),
			Resources:    make([]any, 0, len(users)),
		}
		for _, u := range users {
			out.Resources = append(out.Resources, u)
		}
		h.writeJSON(w, r, http.StatusOK, out)
	case http.MethodPost:
		var u User
		if !h.decodeResource(w, r, &u, SchemaUser) {
			return
		}
		created, err := h.store.CreateUser(r.Context(), u)
		if err != nil {
			h.internalError(w, r, "create user", err)
			return
		}
		h.writeJSON(w, r, http.StatusCreated, created)
	default:
		w.Header().Set("Allow", "GET, POST")
		h.writeErr(w, r, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

type userItemHandler struct{ handlerDeps }

func (h userItemHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := h.itemID(w, r, "/Users/")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r, id)
	case http.MethodPut:
		h.handlePut(w, r, id)
	case http.MethodDelete:
		h.handleDelete(w, r, id)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		h.writeErr(w, r, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

func (h userItemHandler) handleGet(w http.ResponseWriter, r *http.Request, id string) {
	u, err := h.store.GetUser(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		h.writeErr(w, r, http.StatusNotFound, "user not found", "")
		return
	}
	if err != nil {
		h.internalError(w, r, "get user", err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, u)
}

func (h userItemHandler) handlePut(w http.ResponseWriter, r *http.Request, id string) {
	var u User
	if !h.decodeResource(w, r, &u, SchemaUser) {
		return
	}
	u.ID = id
	updated, err := h.store.UpdateUser(r.Context(), u)
	if errors.Is(err, ErrNotFound) {
		h.writeErr(w, r, http.StatusNotFound, "user not found", "")
		return
	}
	if err != nil {
		h.internalError(w, r, "update user", err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, updated)
}

func (h userItemHandler) handleDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.store.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeErr(w, r, http.StatusNotFound, "user not found", "")
			return
		}
		h.internalError(w, r, "delete user", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Groups (mirrors the Users handlers) ---

type groupListHandler struct{ handlerDeps }

func (h groupListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		start, count := paging(r)
		groups, total, err := h.store.ListGroups(r.Context(), start, count, r.URL.Query().Get("filter"))
		if err != nil {
			h.internalError(w, r, "list groups", err)
			return
		}
		groups = capPage(groups, count)
		out := ListResponse{
			Schemas:      []string{SchemaList},
			TotalResults: total,
			StartIndex:   start,
			ItemsPerPage: len(groups),
			Resources:    make([]any, 0, len(groups)),
		}
		for _, g := range groups {
			out.Resources = append(out.Resources, g)
		}
		h.writeJSON(w, r, http.StatusOK, out)
	case http.MethodPost:
		var g Group
		if !h.decodeResource(w, r, &g, SchemaGroup) {
			return
		}
		created, err := h.store.CreateGroup(r.Context(), g)
		if err != nil {
			h.internalError(w, r, "create group", err)
			return
		}
		h.writeJSON(w, r, http.StatusCreated, created)
	default:
		w.Header().Set("Allow", "GET, POST")
		h.writeErr(w, r, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

type groupItemHandler struct{ handlerDeps }

func (h groupItemHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := h.itemID(w, r, "/Groups/")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r, id)
	case http.MethodPut:
		h.handlePut(w, r, id)
	case http.MethodDelete:
		h.handleDelete(w, r, id)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		h.writeErr(w, r, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

func (h groupItemHandler) handleGet(w http.ResponseWriter, r *http.Request, id string) {
	g, err := h.store.GetGroup(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		h.writeErr(w, r, http.StatusNotFound, "group not found", "")
		return
	}
	if err != nil {
		h.internalError(w, r, "get group", err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, g)
}

func (h groupItemHandler) handlePut(w http.ResponseWriter, r *http.Request, id string) {
	var g Group
	if !h.decodeResource(w, r, &g, SchemaGroup) {
		return
	}
	g.ID = id
	updated, err := h.store.UpdateGroup(r.Context(), g)
	if errors.Is(err, ErrNotFound) {
		h.writeErr(w, r, http.StatusNotFound, "group not found", "")
		return
	}
	if err != nil {
		h.internalError(w, r, "update group", err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, updated)
}

func (h groupItemHandler) handleDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.store.DeleteGroup(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeErr(w, r, http.StatusNotFound, "group not found", "")
			return
		}
		h.internalError(w, r, "delete group", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

// itemID extracts the resource id from r.URL.Path by trimming prefix (e.g.
// "/Users/") and rejects it — writing the SCIM invalidPath error to w and
// returning ok=false — when it is empty or contains a further path segment.
func (h handlerDeps) itemID(w http.ResponseWriter, r *http.Request, prefix string) (id string, ok bool) {
	id = strings.TrimPrefix(r.URL.Path, prefix)
	if id == "" || strings.Contains(id, "/") {
		h.writeErr(w, r, http.StatusBadRequest, "invalid id", "invalidPath")
		return "", false
	}
	return id, true
}

func paging(r *http.Request) (start, count int) {
	start = 1
	count = defaultPageSize
	if s := r.URL.Query().Get("startIndex"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			start = n
		}
	}
	if s := r.URL.Query().Get("count"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			switch {
			case n <= 0:
				count = 0
			case n > maxPageSize:
				count = maxPageSize
			default:
				count = n
			}
		}
	}
	return start, count
}

func capPage[T any](resources []T, count int) []T {
	if len(resources) > count {
		return resources[:count]
	}
	return resources
}

func (h handlerDeps) decodeResource(w http.ResponseWriter, r *http.Request, dst any, requiredSchema string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		h.writeDecodeError(w, r, err)
		return false
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errTrailingJSON
		}
		h.writeDecodeError(w, r, err)
		return false
	}
	if !containsSchema(resourceSchemas(dst), requiredSchema) {
		h.writeErr(w, r, http.StatusBadRequest, "required schema missing", "invalidValue")
		return false
	}
	return true
}

func resourceSchemas(dst any) []string {
	switch resource := dst.(type) {
	case *User:
		return resource.Schemas
	case *Group:
		return resource.Schemas
	default:
		return nil
	}
}

func containsSchema(schemas []string, required string) bool {
	return slices.Contains(schemas, required)
}

func (h handlerDeps) writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		h.writeErr(w, r, http.StatusRequestEntityTooLarge, "request body too large", "")
		return
	}
	h.writeErr(w, r, http.StatusBadRequest, "invalid body", "invalidSyntax")
}

func (h handlerDeps) internalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	h.logger.ErrorContext(
		r.Context(),
		"auth/scim: store operation failed",
		slog.String("operation", operation),
		slog.Any("error", err),
	)
	h.writeErr(w, r, http.StatusInternalServerError, "internal server error", "")
}

func (h handlerDeps) writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", mediaTypeKey)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.ErrorContext(r.Context(), "auth/scim: encode response failed", slog.Any("error", err))
	}
}

func (h handlerDeps) writeErr(w http.ResponseWriter, r *http.Request, status int, detail, scimType string) {
	h.writeJSON(w, r, status, Error{
		Schemas: []string{SchemaError},
		Status:  strconv.Itoa(status),
		Detail:  detail,
		ScimErr: scimType,
	})
}
