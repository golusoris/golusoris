// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/grpc"
)

// GRPCModule chains [UnaryServerInterceptor] into the golusoris gRPC server
// (grpc.Module), over the [Store] and [Config] that [Module] provides.
// Streaming RPCs are untouched; add [StreamServerInterceptor] explicitly to
// reject keyed streams.
var GRPCModule = fx.Module(
	"golusoris.idempotency.grpc",
	grpc.ProvideServerOptionFn(newGRPCServerOption),
)
