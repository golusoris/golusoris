// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/golusoris/golusoris/idempotency"
	"github.com/golusoris/golusoris/tenancy"
)

const testMethod = "/payments.v1.Payments/Charge"

// countingHandler answers with "<request>-done" and counts executions.
type countingHandler struct {
	calls atomic.Int32
	err   error
}

func (h *countingHandler) handle(_ context.Context, req any) (any, error) {
	h.calls.Add(1)
	if h.err != nil {
		return nil, h.err
	}
	return wrapperspb.String(req.(*wrapperspb.StringValue).GetValue() + "-done"), nil
}

func keyed() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(idempotency.DefaultGRPCMetadata, "k"))
}

func callUnary(
	ctx context.Context,
	interceptor grpc.UnaryServerInterceptor,
	method string,
	req any,
	handler grpc.UnaryHandler,
) (any, error) {
	return interceptor(ctx, req, &grpc.UnaryServerInfo{FullMethod: method}, handler)
}

func requireCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("status code = %s (%v); want %s", got, err, want)
	}
}

func TestUnaryServerInterceptor_ReplaysCompletedCall(t *testing.T) {
	t.Parallel()
	handler := &countingHandler{}
	interceptor := idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{})
	first, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), handler.handle)
	require.NoError(t, err)
	second, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), handler.handle)
	require.NoError(t, err)
	replayed, ok := second.(*wrapperspb.StringValue)
	require.True(t, ok, "replay type = %T; want *wrapperspb.StringValue", second)
	require.True(t, proto.Equal(first.(proto.Message), replayed))
	require.EqualValues(t, 1, handler.calls.Load())
}

func TestUnaryServerInterceptor_MissingKey(t *testing.T) {
	t.Parallel()
	for name, required := range map[string]bool{"optional": false, "required": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			handler := &countingHandler{}
			interceptor := idempotency.UnaryServerInterceptor(
				idempotency.NewMemoryStore(), idempotency.GRPCOptions{Required: required})
			_, err := callUnary(context.Background(), interceptor, testMethod, wrapperspb.String("pay"), handler.handle)
			if required {
				requireCode(t, err, codes.InvalidArgument)
				require.Zero(t, handler.calls.Load())
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 1, handler.calls.Load())
		})
	}
}

func TestUnaryServerInterceptor_RejectsKeyReuseWithDifferentRequest(t *testing.T) {
	t.Parallel()
	handler := &countingHandler{}
	interceptor := idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{})
	_, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("first"), handler.handle)
	require.NoError(t, err)
	_, err = callUnary(keyed(), interceptor, testMethod, wrapperspb.String("second"), handler.handle)
	requireCode(t, err, codes.FailedPrecondition)
	require.Contains(t, err.Error(), idempotency.ErrFingerprintMismatch.Error())
	require.EqualValues(t, 1, handler.calls.Load())
}

func TestUnaryServerInterceptor_InFlightConflicts(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	blocking := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		calls.Add(1)
		close(entered)
		<-release
		return wrapperspb.String("done"), nil
	})
	interceptor := idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{})
	firstErr := make(chan error, 1)
	go func() {
		_, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), blocking)
		firstErr <- err
	}()
	<-entered
	_, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), blocking)
	close(release)
	requireCode(t, err, codes.Aborted)
	require.NoError(t, <-firstErr)
	require.EqualValues(t, 1, calls.Load())
}

func TestUnaryServerInterceptor_ReplaysClientFaultStatus(t *testing.T) {
	t.Parallel()
	handler := &countingHandler{err: status.Error(codes.NotFound, "no such account")}
	interceptor := idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{})
	for range 2 {
		resp, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), handler.handle)
		require.Nil(t, resp)
		requireCode(t, err, codes.NotFound)
		require.Equal(t, "no such account", status.Convert(err).Message())
	}
	require.EqualValues(t, 1, handler.calls.Load())
}

func TestUnaryServerInterceptor_ServerFaultIsNotCached(t *testing.T) {
	t.Parallel()
	for name, handlerErr := range map[string]error{
		"unavailable": status.Error(codes.Unavailable, "down"),
		"aborted":     status.Error(codes.Aborted, "retry"),
		"plain error": errors.New("boom"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			handler := &countingHandler{err: handlerErr}
			interceptor := idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{})
			for range 2 {
				_, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), handler.handle)
				require.ErrorIs(t, err, handlerErr)
			}
			require.EqualValues(t, 2, handler.calls.Load())
		})
	}
}

func TestUnaryServerInterceptor_Bounds(t *testing.T) {
	t.Parallel()
	request := wrapperspb.String("12345")
	t.Run("request over limit", func(t *testing.T) {
		t.Parallel()
		handler := &countingHandler{}
		limit := int64(proto.Size(request) - 1)
		interceptor := idempotency.UnaryServerInterceptor(
			idempotency.NewMemoryStore(), idempotency.GRPCOptions{MaxRequestBody: limit})
		_, err := callUnary(keyed(), interceptor, testMethod, request, handler.handle)
		requireCode(t, err, codes.ResourceExhausted)
		require.Zero(t, handler.calls.Load())
	})
	t.Run("request at limit", func(t *testing.T) {
		t.Parallel()
		handler := &countingHandler{}
		interceptor := idempotency.UnaryServerInterceptor(
			idempotency.NewMemoryStore(), idempotency.GRPCOptions{MaxRequestBody: int64(proto.Size(request))})
		_, err := callUnary(keyed(), interceptor, testMethod, request, handler.handle)
		require.NoError(t, err)
	})
	t.Run("response overflow is delivered but not cached", func(t *testing.T) {
		t.Parallel()
		handler := &countingHandler{}
		interceptor := idempotency.UnaryServerInterceptor(
			idempotency.NewMemoryStore(), idempotency.GRPCOptions{MaxResponseBody: 4})
		for range 2 {
			resp, err := callUnary(keyed(), interceptor, testMethod, request, handler.handle)
			require.NoError(t, err)
			require.Equal(t, "12345-done", resp.(*wrapperspb.StringValue).GetValue())
		}
		require.EqualValues(t, 2, handler.calls.Load())
	})
}

func TestUnaryServerInterceptor_RejectsInvalidInput(t *testing.T) {
	t.Parallel()
	handler := &countingHandler{}
	interceptor := idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{})
	_, err := callUnary(keyed(), interceptor, testMethod, "not a proto", handler.handle)
	requireCode(t, err, codes.Internal)
	twoKeys := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(idempotency.DefaultGRPCMetadata, "a", idempotency.DefaultGRPCMetadata, "b"))
	_, err = callUnary(twoKeys, interceptor, testMethod, wrapperspb.String("pay"), handler.handle)
	requireCode(t, err, codes.InvalidArgument)
	require.Zero(t, handler.calls.Load())
}

// claimStubStore fails every call, or returns a fixed claim when set.
type claimStubStore struct {
	claim *idempotency.ClaimResult
}

func (s claimStubStore) Claim(context.Context, string, string, time.Duration) (idempotency.ClaimResult, error) {
	if s.claim != nil {
		return *s.claim, nil
	}
	return idempotency.ClaimResult{}, errors.New("store down")
}

func (claimStubStore) Commit(context.Context, string, string, string, idempotency.CachedResponse, time.Duration) error {
	return errors.New("store down")
}

func (claimStubStore) Release(context.Context, string, string) error { return errors.New("store down") }

func TestUnaryServerInterceptor_StoreFailures(t *testing.T) {
	t.Parallel()
	request := wrapperspb.String("pay")
	fingerprint := func() string {
		store := &recordingStore{Store: idempotency.NewMemoryStore()}
		interceptor := idempotency.UnaryServerInterceptor(store, idempotency.GRPCOptions{})
		_, _ = callUnary(keyed(), interceptor, testMethod, request, (&countingHandler{}).handle)
		return store.fingerprint
	}()
	corrupt := func(header string, body []byte) idempotency.Store {
		return claimStubStore{claim: &idempotency.ClaimResult{
			State:       idempotency.ClaimCompleted,
			Fingerprint: fingerprint,
			Response: idempotency.CachedResponse{
				Header: http.Header{"Grpc-Status": {header}},
				Body:   body,
			},
		}}
	}
	tests := map[string]struct {
		store idempotency.Store
		want  codes.Code
	}{
		"nil store":              {store: nil, want: codes.Unavailable},
		"claim error":            {store: claimStubStore{}, want: codes.Unavailable},
		"acquired without token": {store: claimStubStore{claim: &idempotency.ClaimResult{State: idempotency.ClaimAcquired}}, want: codes.Internal},
		"unknown state":          {store: claimStubStore{claim: &idempotency.ClaimResult{State: 99}}, want: codes.Internal},
		"bad status header":      {store: corrupt("x", nil), want: codes.Internal},
		"bad status body":        {store: corrupt("5", []byte{0xff}), want: codes.Internal},
		"bad message body":       {store: corrupt("0", []byte{0xff}), want: codes.Internal},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			handler := &countingHandler{}
			interceptor := idempotency.UnaryServerInterceptor(tc.store, idempotency.GRPCOptions{})
			_, err := callUnary(keyed(), interceptor, testMethod, request, handler.handle)
			requireCode(t, err, tc.want)
			require.Zero(t, handler.calls.Load())
		})
	}
}

// recordingStore captures the fingerprint the interceptor claims with.
type recordingStore struct {
	idempotency.Store

	fingerprint string
}

func (s *recordingStore) Claim(ctx context.Context, key, fingerprint string, ttl time.Duration) (idempotency.ClaimResult, error) {
	s.fingerprint = fingerprint
	return s.Store.Claim(ctx, key, fingerprint, ttl)
}

func TestUnaryServerInterceptor_CommitFailureStillAnswers(t *testing.T) {
	t.Parallel()
	store := claimStubStore{claim: &idempotency.ClaimResult{State: idempotency.ClaimAcquired, Token: "t"}}
	handler := &countingHandler{}
	interceptor := idempotency.UnaryServerInterceptor(store, idempotency.GRPCOptions{})
	resp, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), handler.handle)
	require.NoError(t, err)
	require.Equal(t, "pay-done", resp.(*wrapperspb.StringValue).GetValue())
}

func TestUnaryServerInterceptor_Scopes(t *testing.T) {
	t.Parallel()
	handler := &countingHandler{}
	scope := idempotency.NewGRPCScopeFunc(func(ctx context.Context, _ string) (string, error) {
		return strings.Join(metadata.ValueFromIncomingContext(ctx, "x-principal"), ","), nil
	})
	interceptor := idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{Scope: scope})
	principal := func(ctx context.Context, name string) context.Context {
		md, _ := metadata.FromIncomingContext(ctx)
		return metadata.NewIncomingContext(ctx, metadata.Join(md, metadata.Pairs("x-principal", name)))
	}
	calls := []context.Context{
		principal(keyed(), "alice"),
		principal(keyed(), "bob"),
		principal(tenantContext(t, keyed(), "tenant-a"), "alice"),
	}
	for _, ctx := range calls {
		_, err := callUnary(ctx, interceptor, testMethod, wrapperspb.String("pay"), handler.handle)
		require.NoError(t, err)
	}
	_, err := callUnary(principal(keyed(), "alice"), interceptor, "/payments.v1.Payments/Refund",
		wrapperspb.String("pay"), handler.handle)
	require.NoError(t, err)
	require.EqualValues(t, 4, handler.calls.Load(), "principal, tenant and method each scope the key")
}

func TestUnaryServerInterceptor_ScopeErrorFails(t *testing.T) {
	t.Parallel()
	handler := &countingHandler{}
	scope := idempotency.NewGRPCScopeFunc(func(context.Context, string) (string, error) {
		return "", errors.New("no principal")
	})
	interceptor := idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{Scope: scope})
	_, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), handler.handle)
	requireCode(t, err, codes.Internal)
	require.Zero(t, handler.calls.Load())
	require.Nil(t, idempotency.NewGRPCScopeFunc(nil))
}

// tenantContext returns parent carrying tenantID the way tenancy.Middleware sets it.
func tenantContext(t *testing.T, parent context.Context, tenantID string) context.Context {
	t.Helper()
	tenants := tenancy.NewMemoryStore()
	require.NoError(t, tenants.Add(tenancy.Tenant{ID: tenantID}))
	captured := make(chan context.Context, 1)
	handler := tenancy.Middleware(tenancy.HeaderExtractor("X-Tenant-ID"), tenants)(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) { captured <- r.Context() },
	))
	req := httptest.NewRequestWithContext(parent, http.MethodPost, "/", nil)
	req.Header.Set("X-Tenant-Id", tenantID)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	require.Len(t, captured, 1, "tenancy middleware reached the handler")
	return <-captured
}

func TestUnaryServerInterceptor_PanicReleasesReservation(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	panicky := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		return wrapperspb.String("ok"), nil
	})
	interceptor := idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{})
	func() {
		defer func() { _ = recover() }()
		_, _ = callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), panicky)
	}()
	_, err := callUnary(keyed(), interceptor, testMethod, wrapperspb.String("pay"), panicky)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())
}

// fakeStream carries only the context the stream interceptor reads.
type fakeStream struct {
	grpc.ServerStream

	ctx context.Context
}

func (s fakeStream) Context() context.Context { return s.ctx }

func TestStreamServerInterceptor(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		ctx    context.Context
		want   codes.Code
		called bool
	}{
		"keyless passes":      {ctx: context.Background(), want: codes.OK, called: true},
		"keyed is rejected":   {ctx: keyed(), want: codes.InvalidArgument},
		"two keys rejected":   {ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(idempotency.DefaultGRPCMetadata, "a", idempotency.DefaultGRPCMetadata, "b")), want: codes.InvalidArgument},
		"required keyless ok": {ctx: context.Background(), want: codes.OK, called: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			called := false
			interceptor := idempotency.StreamServerInterceptor(idempotency.GRPCOptions{Required: true})
			err := interceptor(nil, fakeStream{ctx: tc.ctx}, &grpc.StreamServerInfo{FullMethod: testMethod},
				func(any, grpc.ServerStream) error {
					called = true
					return nil
				})
			requireCode(t, err, tc.want)
			require.Equal(t, tc.called, called)
		})
	}
}

func TestGRPCOptions_areComparable(t *testing.T) {
	t.Parallel()
	opts := idempotency.GRPCOptions{Metadata: idempotency.DefaultGRPCMetadata}
	set := map[idempotency.GRPCOptions]struct{}{opts: {}}
	_, ok := set[opts]
	require.True(t, ok)
}
