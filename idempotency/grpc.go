// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/tenancy"
)

// DefaultGRPCMetadata is the incoming metadata key carrying the idempotency key.
const DefaultGRPCMetadata = "idempotency-key"

// grpcStatusHeader marks a stored gRPC outcome; "0" holds a response message.
const grpcStatusHeader = "Grpc-Status"

// errNotProtoMessage reports a request or response outside the protobuf codec.
var errNotProtoMessage = errors.New("idempotency: message is not a protobuf message")

// GRPCScopeFunc returns an additional caller scope for a gRPC idempotency key.
type GRPCScopeFunc func(ctx context.Context, fullMethod string) (string, error)

// NewGRPCScopeFunc stores scope behind a comparable pointer for
// [GRPCOptions]. A nil callback returns nil.
func NewGRPCScopeFunc(scope GRPCScopeFunc) *GRPCScopeFunc {
	if scope == nil {
		return nil
	}
	return &scope
}

// GRPCOptions tunes the gRPC interceptors.
type GRPCOptions struct {
	// Metadata is the incoming metadata key carrying the idempotency key.
	// Default: [DefaultGRPCMetadata].
	Metadata string
	// TTL is how long reservations and completed responses are retained.
	// Default: 24h.
	TTL time.Duration
	// Required, when true, rejects unary calls without the key
	// (codes.InvalidArgument).
	Required bool
	// MaxRequestBody bounds the serialized request size fingerprinted.
	// Default: 1 MiB.
	MaxRequestBody int64
	// MaxResponseBody bounds the serialized response retained for replay.
	// Larger responses reach the first caller but are not cached. Default: 1 MiB.
	MaxResponseBody int64
	// Scope adds a principal or application scope to the built-in method and
	// tenancy-ID scope.
	Scope *GRPCScopeFunc
	// Logger receives store failures. nil uses slog.Default().
	Logger *slog.Logger
}

func (o *GRPCOptions) defaults() {
	if o.Metadata == "" {
		o.Metadata = DefaultGRPCMetadata
	}
	if o.TTL <= 0 {
		o.TTL = 24 * time.Hour
	}
	if o.MaxRequestBody <= 0 {
		o.MaxRequestBody = defaultBodyLimit
	}
	if o.MaxResponseBody <= 0 {
		o.MaxResponseBody = defaultBodyLimit
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Scope != nil && *o.Scope != nil {
		scope := *o.Scope
		o.Scope = &scope
	}
}

// grpcCacheable maps the deterministic, client-fault codes that are stored
// and replayed to their HTTP equivalent; every other code releases the key.
var grpcCacheable = map[codes.Code]int{
	codes.OK:                 http.StatusOK,
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.NotFound:           http.StatusNotFound,
	codes.AlreadyExists:      http.StatusConflict,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.FailedPrecondition: http.StatusBadRequest,
	codes.OutOfRange:         http.StatusBadRequest,
	codes.Unauthenticated:    http.StatusUnauthorized,
}

// UnaryServerInterceptor enforces idempotency keys on unary RPCs with the
// semantics of [Middleware]: the first call for a scoped key runs the handler
// and stores its outcome; a completed retry replays it without running the
// handler; a concurrent retry fails with codes.Aborted; key reuse with a
// different request fails with codes.FailedPrecondition. Server-fault codes
// are not stored, so the client may retry them.
func UnaryServerInterceptor(store Store, opts GRPCOptions) grpc.UnaryServerInterceptor {
	opts.defaults()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		rawKey, err := grpcKey(ctx, opts.Metadata)
		if err != nil {
			return nil, err
		}
		if rawKey == "" {
			if opts.Required {
				return nil, status.Errorf(codes.InvalidArgument, "idempotency: missing %s metadata", opts.Metadata)
			}
			return handler(ctx, req)
		}
		if validate.IsNil(store) || validate.IsNil(handler) {
			return nil, grpcStatus(codes.Unavailable, "idempotency: store error")
		}
		call := grpcCall{store: store, opts: opts, handler: handler, req: req}
		return call.serve(ctx, info.FullMethod, rawKey)
	}
}

// StreamServerInterceptor rejects streaming RPCs that carry an idempotency
// key with codes.InvalidArgument, since a stream cannot be replayed; keyless
// streams pass through unchanged and Required does not apply.
func StreamServerInterceptor(opts GRPCOptions) grpc.StreamServerInterceptor {
	opts.defaults()
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		rawKey, err := grpcKey(stream.Context(), opts.Metadata)
		if err != nil {
			return err
		}
		if rawKey != "" {
			return grpcStatus(codes.InvalidArgument, "idempotency: streaming RPCs cannot be replayed")
		}
		return handler(srv, stream)
	}
}

type grpcCall struct {
	store   Store
	opts    GRPCOptions
	handler grpc.UnaryHandler
	req     any
}

func (c grpcCall) serve(ctx context.Context, fullMethod, rawKey string) (any, error) {
	fingerprint, err := grpcFingerprint(c.req, c.opts.MaxRequestBody)
	if errors.Is(err, ErrRequestBodyTooLarge) {
		return nil, grpcStatus(codes.ResourceExhausted, ErrRequestBodyTooLarge.Error())
	}
	if err != nil {
		return nil, grpcStatus(codes.Internal, "idempotency: request fingerprint error")
	}
	key, err := grpcScopedKey(ctx, fullMethod, rawKey, c.opts.Scope)
	if err != nil {
		return nil, grpcStatus(codes.Internal, "idempotency: scope error")
	}
	claim, err := c.store.Claim(ctx, key, fingerprint, c.opts.TTL)
	if err != nil {
		return nil, grpcStatus(codes.Unavailable, "idempotency: store error")
	}
	return c.answer(ctx, key, fingerprint, claim)
}

func (c grpcCall) answer(ctx context.Context, key, fingerprint string, claim ClaimResult) (any, error) {
	switch claim.State {
	case ClaimAcquired:
		if claim.Token == "" {
			return nil, grpcStatus(codes.Internal, "idempotency: invalid store claim")
		}
		return c.runClaimed(ctx, key, fingerprint, claim.Token)
	case ClaimInFlight:
		return nil, grpcStatus(codes.Aborted, ErrConflict.Error())
	case ClaimFingerprintMismatch:
		return nil, grpcStatus(codes.FailedPrecondition, ErrFingerprintMismatch.Error())
	case ClaimCompleted:
		if claim.Fingerprint != fingerprint {
			return nil, grpcStatus(codes.FailedPrecondition, ErrFingerprintMismatch.Error())
		}
		return replayGRPC(claim.Response)
	default:
		return nil, grpcStatus(codes.Internal, "idempotency: invalid store claim")
	}
}

func (c grpcCall) runClaimed(ctx context.Context, key, fingerprint, token string) (any, error) {
	held := true
	defer func() {
		if held {
			releaseClaim(ctx, c.store, key, token, c.opts.Logger)
		}
	}()
	resp, handlerErr := c.handler(ctx, c.req)
	cached, ok := encodeGRPCOutcome(resp, handlerErr, c.opts.MaxResponseBody)
	if !ok {
		return resp, handlerErr
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeWriteTimeout)
	defer cancel()
	if err := c.store.Commit(commitCtx, key, token, fingerprint, cached, c.opts.TTL); err != nil {
		c.opts.Logger.WarnContext(commitCtx, "idempotency: commit grpc response", slog.Any("err", err))
		return resp, handlerErr
	}
	held = false
	return resp, handlerErr
}

// grpcStatus builds the status error the client receives.
func grpcStatus(code codes.Code, msg string) error {
	return status.Error(code, msg) //nolint:wrapcheck // a gRPC status is the wire response; wrapping would hide its code
}

func grpcKey(ctx context.Context, name string) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, name)
	switch len(values) {
	case 0:
		return "", nil
	case 1:
		return values[0], nil
	default:
		return "", status.Errorf(codes.InvalidArgument, "idempotency: multiple %s values", name)
	}
}

func grpcFingerprint(req any, limit int64) (string, error) {
	message, ok := req.(proto.Message)
	if !ok || validate.IsNil(message) {
		return "", errNotProtoMessage
	}
	if int64(proto.Size(message)) > limit {
		return "", ErrRequestBodyTooLarge
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("idempotency: marshal request: %w", err)
	}
	return fingerprintPayload(string(proto.MessageName(message)), raw)
}

func grpcScopedKey(ctx context.Context, fullMethod, rawKey string, scope *GRPCScopeFunc) (string, error) {
	extraScope := ""
	if scope != nil {
		if *scope == nil {
			return "", errors.New("idempotency: nil scope callback")
		}
		var err error
		extraScope, err = (*scope)(ctx, fullMethod)
		if err != nil {
			return "", fmt.Errorf("idempotency: resolve scope: %w", err)
		}
	}
	tenantID := ""
	if tenant, ok := tenancy.FromContext(ctx); ok {
		tenantID = tenant.ID
	}
	return digestScope("grpc:v1:", fullMethod, tenantID, extraScope, rawKey)
}

// encodeGRPCOutcome stores a response message as anypb.Any and a status
// error as its google.rpc.Status; ok is false for outcomes never replayed.
func encodeGRPCOutcome(resp any, handlerErr error, limit int64) (CachedResponse, bool) {
	code := codes.OK
	var body []byte
	var err error
	if handlerErr != nil {
		st, isStatus := status.FromError(handlerErr)
		if !isStatus {
			return CachedResponse{}, false
		}
		code = st.Code()
		body, err = proto.Marshal(st.Proto())
	} else {
		body, err = marshalAny(resp)
	}
	httpStatus, cacheable := grpcCacheable[code]
	if err != nil || !cacheable || int64(len(body)) > limit {
		return CachedResponse{}, false
	}
	return CachedResponse{
		StatusCode: httpStatus,
		Header:     http.Header{grpcStatusHeader: []string{strconv.Itoa(int(code))}},
		Body:       body,
	}, true
}

func marshalAny(resp any) ([]byte, error) {
	message, ok := resp.(proto.Message)
	if !ok || validate.IsNil(message) {
		return nil, errNotProtoMessage
	}
	wrapped, err := anypb.New(message)
	if err != nil {
		return nil, fmt.Errorf("idempotency: wrap response: %w", err)
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(wrapped)
	if err != nil {
		return nil, fmt.Errorf("idempotency: marshal response: %w", err)
	}
	return raw, nil
}

func replayGRPC(response CachedResponse) (any, error) {
	code, err := strconv.ParseUint(response.Header.Get(grpcStatusHeader), 10, 32)
	if err != nil {
		return nil, grpcStatus(codes.Internal, "idempotency: invalid stored response")
	}
	if codes.Code(code) != codes.OK {
		stored := status.New(codes.Unknown, "").Proto()
		if proto.Unmarshal(response.Body, stored) != nil || status.FromProto(stored).Code() != codes.Code(code) {
			return nil, grpcStatus(codes.Internal, "idempotency: invalid stored response")
		}
		return nil, status.ErrorProto(stored) //nolint:wrapcheck // the stored status is the wire response; wrapping would hide its code
	}
	var wrapped anypb.Any
	if proto.Unmarshal(response.Body, &wrapped) != nil {
		return nil, grpcStatus(codes.Internal, "idempotency: invalid stored response")
	}
	message, err := anypb.UnmarshalNew(&wrapped, proto.UnmarshalOptions{})
	if err != nil {
		return nil, grpcStatus(codes.Internal, "idempotency: stored response type unavailable")
	}
	return message, nil
}

// newGRPCServerOption builds the [GRPCModule] interceptor from module config.
func newGRPCServerOption(store Store, cfg Config, logger *slog.Logger) grpc.ServerOption {
	return grpc.ChainUnaryInterceptor(UnaryServerInterceptor(store, GRPCOptions{
		Metadata:        cfg.GRPC.Metadata,
		TTL:             cfg.TTL,
		Required:        cfg.GRPC.Required,
		MaxRequestBody:  cfg.MaxRequestBody,
		MaxResponseBody: cfg.MaxResponseBody,
		Logger:          logger,
	}))
}
