// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/grpc/credentials"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/tlsx"
)

var errTLSPair = errors.New("grpc: tls requires cert_file and key_file")

// serverCredentials builds TLS 1.3 credentials whose certificate and client CA
// pool reload from disk on handshake.
func serverCredentials(cfg Config, logger *slog.Logger, clk clock.Clock) (credentials.TransportCredentials, error) {
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, errTLSPair
	}
	auth, err := tlsx.ParseClientAuth(cfg.ClientAuth, cfg.CAFile != "")
	if err != nil {
		return nil, fmt.Errorf("grpc: tls client_auth: %w", err)
	}
	files := tlsx.Files{Cert: cfg.CertFile, Key: cfg.KeyFile, CA: cfg.CAFile}
	reloader, err := tlsx.NewReloader(files, tlsx.Options{Clock: clk, Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("grpc: load tls cert: %w", err)
	}
	return credentials.NewTLS(reloader.ServerConfig(auth)), nil
}
