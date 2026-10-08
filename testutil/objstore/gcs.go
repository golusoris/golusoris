// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package objstore

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/internal/testimages"
)

const (
	gcsPort   = "4443/tcp"
	gcsBucket = "conformance"
)

// GCSServer is a running fake-gcs-server holding one empty bucket. It does
// not verify signed-URL signatures or expiry.
type GCSServer struct {
	// Endpoint is the JSON API base for option.WithEndpoint.
	Endpoint string
	Bucket   string
}

// StartGCS boots fake-gcs-server and creates [GCSServer.Bucket]. The public
// host is set to the mapped address, so XML reads, signed URLs, and resumable
// upload sessions route back through the published port.
func StartGCS(t *testing.T) GCSServer {
	t.Helper()
	hostPort := startContainer(t, "gcs", testcontainers.ContainerRequest{
		Image:        testimages.FakeGCSServer,
		ExposedPorts: []string{gcsPort},
		Cmd:          []string{"-scheme", "http", "-port", "4443", "-backend", "memory"},
		WaitingFor:   wait.ForHTTP("/_internal/healthcheck").WithPort(gcsPort),
	}, gcsPort)
	base := "http://" + hostPort
	ctx, cancel := context.WithTimeout(t.Context(), setupTimeout)
	defer cancel()
	emulatorCall(ctx, t, http.MethodPut, base+"/_internal/config",
		fmt.Sprintf(`{"externalUrl":%q,"publicHost":%q}`, base, hostPort))
	emulatorCall(ctx, t, http.MethodPost, base+"/storage/v1/b?project=test",
		fmt.Sprintf(`{"name":%q}`, gcsBucket))
	return GCSServer{Endpoint: base + "/storage/v1/", Bucket: gcsBucket}
}

func emulatorCall(ctx context.Context, t *testing.T, method, target, body string) {
	t.Helper()
	status, resp := send(ctx, t, method, target, http.Header{"Content-Type": {"application/json"}}, []byte(body))
	if status/100 != 2 {
		t.Fatalf("testutil/objstore: %s %s = %d %s", method, target, status, bytes.TrimSpace(resp))
	}
}
