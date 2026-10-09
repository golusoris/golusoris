// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package localstackdev boots a LocalStack KMS and STS emulator via
// testcontainers-go. Docker is required; -short or an unhealthy Docker
// provider skips the test. It lives in this module, not in root testutil,
// so the module's tests need no root golusoris release.
//
//	srv := localstackdev.Start(t)
//	cfg := aws.Config{Key: keyARN, Endpoint: srv.Endpoint, Region: srv.Region}
package localstackdev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	apiPort = "4566/tcp"
	// Region is the region every test key lives in.
	Region = "us-east-1"
	// startTimeout bounds one container start including a cold image pull,
	// as in testutil/pg.
	startTimeout = 5 * time.Minute
	stopTimeout  = 10 * time.Second
	callTimeout  = 60 * time.Second
	readyLog     = "Ready."
	maxBody      = 1 << 20
)

// Server is a running LocalStack with KMS and STS.
type Server struct {
	// Endpoint is the edge URL for every service.
	Endpoint string
	// Region is the region to sign requests for.
	Region string
}

// Start boots LocalStack on a mapped port.
func Start(t *testing.T) Server {
	t.Helper()
	if testing.Short() {
		t.Skip("localstackdev: container-backed; skipped under -short")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Image:        LocalStackImage,
		ReaperImage:  RyukImage,
		ExposedPorts: []string{apiPort},
		Env:          map[string]string{"SERVICES": "kms,sts", "EAGER_SERVICE_LOADING": "1"},
		WaitingFor:   wait.ForLog(readyLog).WithStartupTimeout(startTimeout),
		Started:      true,
	})
	if err != nil {
		t.Fatalf("localstackdev: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTimeout)
		defer stopCancel()
		if termErr := ctr.Terminate(stopCtx); termErr != nil {
			t.Logf("localstackdev: terminate container: %v", termErr)
		}
	})
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("localstackdev: host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, apiPort)
	if err != nil {
		t.Fatalf("localstackdev: mapped port: %v", err)
	}
	srv := Server{Endpoint: "http://" + net.JoinHostPort(host, port.Port()), Region: Region}
	ready(t, srv)
	return srv
}

// ready polls the health endpoint until KMS and STS are available.
func ready(t *testing.T, s Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callTimeout)
	defer cancel()
	var last error
	for range 120 {
		last = health(ctx, s.Endpoint)
		if last == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("localstackdev: not ready: %v", errors.Join(last, ctx.Err()))
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("localstackdev: not ready: %v", last)
}

func health(ctx context.Context, endpoint string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/_localstack/health", http.NoBody)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: callTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	var h struct {
		Services map[string]string `json:"services"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&h); err != nil {
		return fmt.Errorf("decode health: %w", err)
	}
	for _, svc := range []string{"kms", "sts"} {
		if st := h.Services[svc]; st != "available" && st != "running" {
			return fmt.Errorf("%s is %q", svc, st)
		}
	}
	return nil
}
