// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"context"

	"google.golang.org/grpc"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

// readinessHealth serves grpc.health.v1. With a statuspage.Registry wired, a
// Check of the overall service ("") runs the readiness-tagged checks first, so
// gRPC probes agree with /readyz.
type readinessHealth struct {
	*grpchealth.Server
	reg *statuspage.Registry
}

// Check refreshes the overall status from the readiness checks, then answers.
func (h *readinessHealth) Check(ctx context.Context, in *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if in.GetService() == "" && h.reg != nil {
		status := healthpb.HealthCheckResponse_NOT_SERVING
		if health.Serving(h.reg.RunTagged(ctx, health.TagReadiness)) {
			status = healthpb.HealthCheckResponse_SERVING
		}
		// No-op after Shutdown, so a draining server stays NOT_SERVING.
		h.SetServingStatus("", status)
	}
	return h.Server.Check(ctx, in) //nolint:wrapcheck // gRPC status errors must reach the client unwrapped
}

// registerHealth registers the health service when enabled and returns its
// server so the lifecycle can flip it to NOT_SERVING before draining.
func registerHealth(srv *grpc.Server, enabled bool, reg *statuspage.Registry) *grpchealth.Server {
	if !enabled {
		return nil
	}
	hs := grpchealth.NewServer()
	healthpb.RegisterHealthServer(srv, &readinessHealth{Server: hs, reg: reg})
	return hs
}
