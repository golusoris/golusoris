package pkgb

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

// padRyuk copies n small files into the Ryuk container's writable layer so
// that removing it (AutoRemove after exit) takes measurably longer.
func padRyuk(ctx context.Context, cli *testcontainers.DockerClient, id string, n int) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	payload := bytes.Repeat([]byte("x"), 512)
	for i := 0; i < n; i++ {
		hdr := &tar.Header{Name: fmt.Sprintf("pad/%03d/%06d", i/1000, i), Mode: 0o644, Size: int64(len(payload))}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(payload); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	_, err := cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: "/", Content: &buf})
	return err
}
