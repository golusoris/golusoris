// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ogenkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/ogen-go/ogen/validate"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/ogenkit"
)

var (
	errUnreachable = errors.New("upstream unreachable")
	errInvalid     = errors.New("invalid release name")
)

func serveProblem(t *testing.T, target string, err error, opts ...ogenkit.Option) (int, map[string]any, string) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ogenkit.ErrorHandlerWith(logger, opts...)(context.Background(), rr, req, err)
	var body map[string]any
	if decErr := json.Unmarshal(rr.Body.Bytes(), &body); decErr != nil {
		t.Fatalf("decode: %v", decErr)
	}
	return rr.Code, body, rr.Body.String()
}

func TestWithHTTPStatusFixedPublicDetail(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("indexer probe http://10.0.0.5:9696: %w", errUnreachable)
	code, body, raw := serveProblem(t, "/indexers/1/test", err,
		ogenkit.WithHTTPStatus(errUnreachable, http.StatusBadGateway, "indexer unreachable"))

	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", code)
	}
	want := map[string]any{
		"type":          "about:blank",
		"title":         "Bad Gateway",
		"status":        float64(http.StatusBadGateway),
		"detail":        "indexer unreachable",
		"error_message": "indexer unreachable",
		"instance":      "/indexers/1/test",
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %#v, want %#v", key, body[key], value)
		}
	}
	if strings.Contains(raw, "10.0.0.5") {
		t.Errorf("body leaked wrap context: %s", raw)
	}
}

func TestWithHTTPStatusEmptyPublicExposesChain(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("queue: %w", errInvalid)
	code, body, _ := serveProblem(t, "/queue", err,
		ogenkit.WithHTTPStatus(errInvalid, http.StatusUnprocessableEntity, ""))

	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
	if body["detail"] != err.Error() {
		t.Errorf("detail = %v, want %q", body["detail"], err.Error())
	}
}

func TestWithHTTPStatusIgnoresInvalidMappings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opt  ogenkit.Option
	}{
		{"nil target", ogenkit.WithHTTPStatus(nil, http.StatusBadGateway, "x")},
		{"status 399", ogenkit.WithHTTPStatus(errUnreachable, 399, "x")},
		{"status 600", ogenkit.WithHTTPStatus(errUnreachable, 600, "x")},
		{"status 0", ogenkit.WithHTTPStatus(errUnreachable, 0, "x")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, body, _ := serveProblem(t, "/x", errUnreachable, tc.opt)
			if code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500 (mapping ignored)", code)
			}
			if body["detail"] != "internal server error" {
				t.Errorf("detail = %v, want redacted", body["detail"])
			}
		})
	}
}

func TestWithHTTPStatusBoundaryStatuses(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadRequest, 599} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			code, body, _ := serveProblem(t, "/x", errUnreachable,
				ogenkit.WithHTTPStatus(errUnreachable, status, "edge"))
			if code != status {
				t.Errorf("status = %d, want %d", code, status)
			}
			if body["detail"] != "edge" {
				t.Errorf("detail = %v, want edge", body["detail"])
			}
		})
	}
}

func TestWithHTTPStatusUnmatchedFallsThrough(t *testing.T) {
	t.Parallel()
	code, _, _ := serveProblem(t, "/x", gerr.NotFound("user"),
		ogenkit.WithHTTPStatus(errUnreachable, http.StatusBadGateway, "x"))
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestWithHTTPStatusFirstMatchAcrossOptions(t *testing.T) {
	t.Parallel()
	code, _, _ := serveProblem(t, "/x", errUnreachable,
		ogenkit.WithHTTPStatus(errUnreachable, http.StatusBadGateway, "x"),
		ogenkit.WithStatus(errUnreachable, gerr.CodeNotFound))
	if code != http.StatusBadGateway {
		t.Errorf("HTTPStatus first: status = %d, want 502", code)
	}
	code, _, _ = serveProblem(t, "/x", errUnreachable,
		ogenkit.WithStatus(errUnreachable, gerr.CodeNotFound),
		ogenkit.WithHTTPStatus(errUnreachable, http.StatusBadGateway, "x"))
	if code != http.StatusNotFound {
		t.Errorf("Status first: status = %d, want 404", code)
	}
}

func TestWithPathInstance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		target string
		opts   []ogenkit.Option
		want   string
	}{
		{"path only", "/users/42?apikey=secret", []ogenkit.Option{ogenkit.WithPathInstance()}, "/users/42"},
		{"default keeps query", "/users/42?apikey=secret", nil, "/users/42?apikey=secret"},
		{"root path", "/?q=1", []ogenkit.Option{ogenkit.WithPathInstance()}, "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, body, _ := serveProblem(t, tc.target, gerr.NotFound("user"), tc.opts...)
			if body["instance"] != tc.want {
				t.Errorf("instance = %v, want %q", body["instance"], tc.want)
			}
		})
	}
}

func TestWithDecodeDetail(t *testing.T) {
	t.Parallel()
	op := ogenerrors.OperationContext{Name: "createWidget", ID: "CreateWidget"}
	raw := errors.New(`decode "name": unexpected byte 0x7b`)
	cases := []struct {
		name       string
		err        error
		opts       []ogenkit.Option
		wantStatus int
		wantDetail string
	}{
		{
			"request decode", &ogenerrors.DecodeRequestError{OperationContext: op, Err: raw},
			[]ogenkit.Option{ogenkit.WithDecodeDetail("malformed request")},
			http.StatusBadRequest, "malformed request",
		},
		{
			"params decode wrapped", fmt.Errorf("op: %w", &ogenerrors.DecodeParamsError{OperationContext: op, Err: raw}),
			[]ogenkit.Option{ogenkit.WithDecodeDetail("malformed request")},
			http.StatusBadRequest, "malformed request",
		},
		{
			"empty public ignored", &ogenerrors.DecodeRequestError{OperationContext: op, Err: raw},
			[]ogenkit.Option{ogenkit.WithDecodeDetail("")},
			http.StatusBadRequest, (&ogenerrors.DecodeRequestError{OperationContext: op, Err: raw}).Error(),
		},
		{
			"non-decode 4xx untouched", gerr.NotFound("user"),
			[]ogenkit.Option{ogenkit.WithDecodeDetail("malformed request")},
			http.StatusNotFound, "user",
		},
		{
			"validation keeps invalid-params", &ogenerrors.DecodeRequestError{OperationContext: op, Err: &validate.Error{
				Fields: []validate.FieldError{{Name: "name", Error: errors.New("too short")}},
			}},
			[]ogenkit.Option{ogenkit.WithInvalidParams(), ogenkit.WithDecodeDetail("malformed request")},
			http.StatusUnprocessableEntity, "request validation failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, body, _ := serveProblem(t, "/widgets", tc.err, tc.opts...)
			if code != tc.wantStatus {
				t.Errorf("status = %d, want %d", code, tc.wantStatus)
			}
			if body["detail"] != tc.wantDetail {
				t.Errorf("detail = %v, want %q", body["detail"], tc.wantDetail)
			}
		})
	}
}
