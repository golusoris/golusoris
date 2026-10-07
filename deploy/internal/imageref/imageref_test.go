// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package imageref_test

import (
	"strings"
	"testing"

	"github.com/golusoris/golusoris/deploy/internal/imageref"
)

func TestValidateImmutableSHA256(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		image   string
		wantErr bool
	}{
		{name: "digest", image: "ghcr.io/example/app@sha256:" + digest},
		{name: "registry port and tag", image: "localhost:5000/example/app:v1@sha256:" + digest},
		{name: "tag only", image: "ghcr.io/example/app:latest", wantErr: true},
		{name: "empty repository", image: "@sha256:" + digest, wantErr: true},
		{name: "short digest", image: "ghcr.io/example/app@sha256:" + digest[:63], wantErr: true},
		{name: "long digest", image: "ghcr.io/example/app@sha256:" + digest + "a", wantErr: true},
		{name: "uppercase digest", image: "ghcr.io/example/app@sha256:" + strings.Repeat("A", 64), wantErr: true},
		{name: "wrong algorithm", image: "ghcr.io/example/app@sha512:" + digest, wantErr: true},
		{name: "whitespace", image: "ghcr.io/example/my app@sha256:" + digest, wantErr: true},
		{name: "second separator", image: "ghcr.io/example/app@sha256:" + digest + "@sha256:" + digest, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := imageref.ValidateImmutableSHA256(tt.image)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateImmutableSHA256(%q) error = %v, wantErr %v", tt.image, err, tt.wantErr)
			}
		})
	}
}
