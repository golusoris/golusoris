// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testimages

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

const manifestReadLimit = 16 << 10

func TestValidate(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	for _, tt := range []struct {
		name    string
		image   string
		wantErr bool
	}{
		{name: "pinned", image: "example/image:v1@sha256:" + digest},
		{name: "registry port", image: "registry.example:5000/image:v1@sha256:" + digest},
		{name: "tag only", image: "example/image:v1", wantErr: true},
		{name: "missing tag", image: "example/image@sha256:" + digest, wantErr: true},
		{name: "empty tag", image: "example/image:@sha256:" + digest, wantErr: true},
		{name: "short digest", image: "example/image:v1@sha256:" + digest[:63], wantErr: true},
		{name: "uppercase digest", image: "example/image:v1@sha256:" + strings.Repeat("A", 64), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := Validate(tt.image); (err != nil) != tt.wantErr {
				t.Fatalf("Validate(%q) error = %v, wantErr %v", tt.image, err, tt.wantErr)
			}
		})
	}
}

func TestWithPinnedReaper(t *testing.T) {
	t.Parallel()
	req := &testcontainers.GenericContainerRequest{}
	if err := WithPinnedReaper().Customize(req); err != nil {
		t.Fatalf("WithPinnedReaper().Customize() error = %v", err)
	}
	if req.ReaperImage != Ryuk {
		t.Fatalf("reaper image = %q, want %q", req.ReaperImage, Ryuk)
	}
}

func TestRepositoryPinsAreValid(t *testing.T) {
	t.Parallel()
	for _, image := range []string{Postgres, Timescale, TimescaleApache, Redis, NATS, ClickHouse, Redpanda, VersityGW, FakeGCSServer, Azurite, Ryuk, ClamAV} {
		if err := Validate(image); err != nil {
			t.Errorf("Validate(%q) error = %v", image, err)
		}
	}
}

func TestCacheManifestMatchesDefaultPins(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", ".github", "testcontainers-images.txt")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close manifest: %v", closeErr)
		}
	})

	content, err := io.ReadAll(io.LimitReader(file, manifestReadLimit+1))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if len(content) > manifestReadLimit {
		t.Fatalf("manifest exceeds %d bytes", manifestReadLimit)
	}

	want := map[string]bool{
		Postgres:        false,
		Timescale:       false,
		TimescaleApache: false,
		Redis:           false,
		NATS:            false,
		ClickHouse:      false,
		Redpanda:        false,
		VersityGW:       false,
		Ryuk:            false,
	}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		seen, ok := want[line]
		if !ok {
			t.Errorf("unexpected cache manifest image %q", line)
			continue
		}
		if seen {
			t.Errorf("duplicate cache manifest image %q", line)
		}
		want[line] = true
	}
	if err = scanner.Err(); err != nil {
		t.Fatalf("scan manifest: %v", err)
	}
	for image, seen := range want {
		if !seen {
			t.Errorf("cache manifest missing %q", image)
		}
	}
}
