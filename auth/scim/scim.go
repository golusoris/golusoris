// Package scim implements a minimal SCIM 2.0 server for user and group
// provisioning (RFC 7643 + RFC 7644). The package ships HTTP handlers
// mounted under `/scim/v2/`, validating the SCIM JSON envelope and
// delegating CRUD to a pluggable [Store].
//
// Apps mount `scim.Handler(store)` on a router (typically chi) and
// authenticate the route with a bearer token at the middleware layer.
package scim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Standard schema URIs (RFC 7643 §8).
const (
	SchemaUser   = "urn:ietf:params:scim:schemas:core:2.0:User"
	SchemaGroup  = "urn:ietf:params:scim:schemas:core:2.0:Group"
	SchemaList   = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	SchemaError  = "urn:ietf:params:scim:api:messages:2.0:Error"
	mediaTypeKey = "application/scim+json"
)

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

// Handler returns an http.Handler implementing /Users and /Groups.
// Mount it under "/scim/v2/" in your router.
func Handler(s Store) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/Users", userListHandler{s})
	mux.Handle("/Users/", userItemHandler{s})
	mux.Handle("/Groups", groupListHandler{s})
	mux.Handle("/Groups/", groupItemHandler{s})
	return mux
}

// --- Users ---

type userListHandler struct{ s Store }

func (h userListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		start, count := paging(r)
		users, total, err := h.s.ListUsers(r.Context(), start, count, r.URL.Query().Get("filter"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error(), "")
			return
		}
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
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var u User
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid body", "invalidSyntax")
			return
		}
		ensureUserSchema(&u)
		created, err := h.s.CreateUser(r.Context(), u)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error(), "")
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

type userItemHandler struct{ s Store }

func (h userItemHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/Users/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, http.StatusBadRequest, "invalid id", "invalidPath")
		return
	}
	switch r.Method {
	case http.MethodGet:
		u, err := h.s.GetUser(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "user not found", "")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error(), "")
			return
		}
		writeJSON(w, http.StatusOK, u)
	case http.MethodPut:
		var u User
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid body", "invalidSyntax")
			return
		}
		u.ID = id
		ensureUserSchema(&u)
		updated, err := h.s.UpdateUser(r.Context(), u)
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "user not found", "")
			return
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error(), "")
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := h.s.DeleteUser(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeErr(w, http.StatusNotFound, "user not found", "")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error(), "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

// --- Groups (mirrors the Users handlers) ---

type groupListHandler struct{ s Store }

func (h groupListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		start, count := paging(r)
		groups, total, err := h.s.ListGroups(r.Context(), start, count, r.URL.Query().Get("filter"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error(), "")
			return
		}
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
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var g Group
		if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid body", "invalidSyntax")
			return
		}
		ensureGroupSchema(&g)
		created, err := h.s.CreateGroup(r.Context(), g)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error(), "")
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

type groupItemHandler struct{ s Store }

func (h groupItemHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/Groups/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, http.StatusBadRequest, "invalid id", "invalidPath")
		return
	}
	switch r.Method {
	case http.MethodGet:
		g, err := h.s.GetGroup(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "group not found", "")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error(), "")
			return
		}
		writeJSON(w, http.StatusOK, g)
	case http.MethodPut:
		var g Group
		if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid body", "invalidSyntax")
			return
		}
		g.ID = id
		ensureGroupSchema(&g)
		updated, err := h.s.UpdateGroup(r.Context(), g)
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "group not found", "")
			return
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error(), "")
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := h.s.DeleteGroup(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeErr(w, http.StatusNotFound, "group not found", "")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error(), "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

// --- helpers ---

func paging(r *http.Request) (start, count int) {
	start = 1
	count = 100
	if s := r.URL.Query().Get("startIndex"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			start = n
		}
	}
	if s := r.URL.Query().Get("count"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 1000 {
			count = n
		}
	}
	return start, count
}

func ensureUserSchema(u *User) {
	if len(u.Schemas) == 0 {
		u.Schemas = []string{SchemaUser}
	}
}

func ensureGroupSchema(g *Group) {
	if len(g.Schemas) == 0 {
		g.Schemas = []string{SchemaGroup}
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", mediaTypeKey)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, detail, scimType string) {
	writeJSON(w, status, Error{
		Schemas: []string{SchemaError},
		Status:  strconv.Itoa(status),
		Detail:  detail,
		ScimErr: scimType,
	})
}
