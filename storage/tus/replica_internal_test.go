// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	tusd "github.com/tus/tusd/v2/pkg/handler"
)

func TestUploadID_Format(t *testing.T) {
	t.Parallel()
	id, err := newUploadID("replica-1")
	if err != nil {
		t.Fatalf("newUploadID: %v", err)
	}
	tag, tagged := uploadReplica(id)
	if !tagged || tag != "replica-1" {
		t.Fatalf("uploadReplica(%q) = (%q, %v); want (replica-1, true)", id, tag, tagged)
	}
	bareID, err := newUploadID("")
	if err != nil {
		t.Fatalf("newUploadID empty: %v", err)
	}
	if len(bareID) != 32 {
		t.Fatalf("bare id length = %d; want 32", len(bareID))
	}
	if _, tagged = uploadReplica(bareID); tagged {
		t.Fatalf("uploadReplica(%q) tagged=true; want false", bareID)
	}
}

func TestUploadReplica_Table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		id         string
		wantTag    string
		wantTagged bool
	}{
		{"tagged", "node-1_0123456789abcdef0123456789abcdef", "node-1", true},
		{"boundary-1-char", "a_0123456789abcdef0123456789abcdef", "a", true},
		{"boundary-63-chars", strings.Repeat("a", 63) + "_0123456789abcdef0123456789abcdef", strings.Repeat("a", 63), true},
		{"digits-hyphens", "pod-0-9_0123456789abcdef0123456789abcdef", "pod-0-9", true},
		{"legacy-32-hex", "0123456789abcdef0123456789abcdef", "", false},
		{"uppercase-hex", "node-1_0123456789ABCDEF0123456789abcdef", "", false},
		{"31-hex", "node-1_0123456789abcdef0123456789abcde", "", false},
		{"33-hex", "node-1_0123456789abcdef0123456789abcdef0", "", false},
		{"empty-tag", "_0123456789abcdef0123456789abcdef", "", false},
		{"tag-with-underscore", "a_b_0123456789abcdef0123456789abcdef", "", false},
		{"tag-with-slash", "a/b_0123456789abcdef0123456789abcdef", "", false},
		{"64-byte-tag", strings.Repeat("a", 64) + "_0123456789abcdef0123456789abcdef", "", false},
		{"empty-id", "", "", false},
		{"non-hex", "node-1_0123456789abcdef0123456789abcdeg", "", false},
		{"invalid-char-in-tag", "node@1_0123456789abcdef0123456789abcdef", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotTag, gotTagged := uploadReplica(tc.id)
			if gotTagged != tc.wantTagged || gotTag != tc.wantTag {
				t.Fatalf("uploadReplica(%q) = (%q, %v); want (%q, %v)",
					tc.id, gotTag, gotTagged, tc.wantTag, tc.wantTagged)
			}
		})
	}
}

func TestHostnameNormalization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"pod-name", "my-pod-0", "my-pod-0", false},
		{"uppercase-dot", "MY-POD.cluster.local", "my-pod-cluster-local", false},
		{"symbols-to-hyphens", "host_name#42", "host-name-42", false},
		{"trim-hyphens", "--my-pod--", "my-pod", false},
		{"cap-63", strings.Repeat("a", 70), strings.Repeat("a", 63), false},
		{"cap-63-trim-end", strings.Repeat("a", 62) + "-overflow", strings.Repeat("a", 62), false},
		{"empty", "", "", true},
		{"all-invalid", "....---....", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeHostname(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("normalizeHostname(%q) err = %v; wantErr=%v", tc.raw, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("normalizeHostname(%q) = %q; want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestConfigTagValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		tag     string
		wantErr bool
	}{
		{"valid", "replica-1", false},
		{"valid-1-char", "a", false},
		{"valid-63-chars", strings.Repeat("a", 63), false},
		{"empty", "", true},
		{"spaces", "   ", true},
		{"hyphens-only", "---", true},
		{"uppercase", "Replica-1", true},
		{"underscore", "replica_1", true},
		{"slash", "replica/1", true},
		{"dot", "replica.1", true},
		{"exceeds-63", strings.Repeat("a", 64), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateConfigTag(tc.tag)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateConfigTag(%q) err = %v; wantErr=%v", tc.tag, err, tc.wantErr)
			}
		})
	}
}

func TestResolveReplicaTag(t *testing.T) {
	t.Parallel()
	got, err := resolveReplicaTag("custom-tag", func() (string, error) {
		return "should-not-be-called", nil
	})
	if err != nil || got != "custom-tag" {
		t.Fatalf("resolveReplicaTag with custom tag = (%q, %v); want (custom-tag, nil)", got, err)
	}

	if _, err = resolveReplicaTag("INVALID_TAG", nil); err == nil {
		t.Fatal("resolveReplicaTag with invalid tag: expected error")
	}

	got, err = resolveReplicaTag("", func() (string, error) {
		return "POD_HOST.local", nil
	})
	if err != nil || got != "pod-host-local" {
		t.Fatalf("resolveReplicaTag from hostname = (%q, %v); want (pod-host-local, nil)", got, err)
	}

	boom := errors.New("hostname failed")
	if _, err = resolveReplicaTag("", func() (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatalf("resolveReplicaTag hostname error = %v; want %v", err, boom)
	}
}

func TestGetUpload_ForeignTag_LogsAndErrors(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	store := newBucketStore(scratch, nil, defaultKeyFunc("uploads/"), logger, nil, "local-node")

	foreignID := "remote-node_0123456789abcdef0123456789abcdef"
	ctx := context.Background()
	_, err = store.GetUpload(ctx, foreignID)
	if err == nil {
		t.Fatal("expected error on foreign tag miss")
	}
	if !errors.Is(err, tusd.ErrNotFound) {
		t.Fatalf("GetUpload err = %v; want errors.Is ErrNotFound", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "remote-node") || !strings.Contains(msg, "local-node") || !strings.Contains(msg, "sticky") {
		t.Fatalf("error message %q missing expected replica tags or sticky warning", msg)
	}
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "tus: upload belongs to another replica") ||
		!strings.Contains(logOutput, foreignID) ||
		!strings.Contains(logOutput, "remote-node") ||
		!strings.Contains(logOutput, "local-node") {
		t.Fatalf("log output %q missing expected warn fields", logOutput)
	}
}

func TestGetUpload_OwnTagAndLegacyMiss_NoWarnLog(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	store := newBucketStore(scratch, nil, defaultKeyFunc("uploads/"), logger, nil, "local-node")

	ctx := context.Background()
	ownMissID := "local-node_0123456789abcdef0123456789abcdef"
	_, err = store.GetUpload(ctx, ownMissID)
	if !errors.Is(err, tusd.ErrNotFound) {
		t.Fatalf("own miss err = %v; want ErrNotFound", err)
	}
	if strings.Contains(err.Error(), "sticky") {
		t.Fatalf("own miss err %q should not mention sticky", err.Error())
	}

	legacyID := "0123456789abcdef0123456789abcdef"
	_, err = store.GetUpload(ctx, legacyID)
	if !errors.Is(err, tusd.ErrNotFound) {
		t.Fatalf("legacy miss err = %v; want ErrNotFound", err)
	}
	if strings.Contains(err.Error(), "sticky") {
		t.Fatalf("legacy miss err %q should not mention sticky", err.Error())
	}
	if logBuf.Len() != 0 {
		t.Fatalf("expected no warn log, got: %s", logBuf.String())
	}
}

func TestHTTP_ForeignTag_PatchAndHead(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	opts.Enabled = true
	opts.ReplicaTag = "node-alpha"
	opts.GracefulRequestCompletionTimeout = 50 * time.Millisecond
	p, _ := newTestParams(t, opts)
	h, err := newHandler(p)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	r := chi.NewRouter()
	h.Mount(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	url := srv.URL + "/files/node-beta_0123456789abcdef0123456789abcdef"

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, url, strings.NewReader("data"))
	if err != nil {
		t.Fatalf("new PATCH request: %v", err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Offset", "0")
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("PATCH status = %d; want 404", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read PATCH body: %v", err)
	}
	if !strings.Contains(string(body), "sticky") || !strings.Contains(string(body), "node-beta") || !strings.Contains(string(body), "node-alpha") {
		t.Fatalf("PATCH body %q missing sticky notice or replica tags", string(body))
	}

	headReq, err := http.NewRequestWithContext(t.Context(), http.MethodHead, url, http.NoBody)
	if err != nil {
		t.Fatalf("new HEAD request: %v", err)
	}
	headReq.Header.Set("Tus-Resumable", "1.0.0")
	headResp, err := srv.Client().Do(headReq)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer func() { _ = headResp.Body.Close() }()
	if headResp.StatusCode != http.StatusNotFound {
		t.Fatalf("HEAD status = %d; want 404", headResp.StatusCode)
	}
}
