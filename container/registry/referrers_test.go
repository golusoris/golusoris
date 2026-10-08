// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/golusoris/golusoris/container/registry"
)

const bundleType = "application/vnd.dev.sigstore.bundle.v0.3+json"

func TestReferrers_TagSchemaFallback(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil) // no referrers API
	c := artifactClient(t, registry.Options{}, nil)
	ctx := testCtx(t)
	subject, err := c.PushArtifact(ctx, host+"/r:model", registry.Artifact{ArtifactType: modelType})
	if err != nil {
		t.Fatalf("push subject: %v", err)
	}
	none, err := c.Referrers(ctx, host+"/r:model", "")
	if err != nil || len(none) != 0 {
		t.Fatalf("Referrers before = %v, %v", none, err)
	}
	for _, typ := range []string{"application/vnd.vmafx.provenance.v1+json", "application/vnd.vmafx.report.v1+json"} {
		if _, err = c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: typ, Subject: &subject}); err != nil {
			t.Fatalf("push referrer %s: %v", typ, err)
		}
	}
	prov, err := c.Referrers(ctx, host+"/r@"+subject.Digest.String(), "application/vnd.vmafx.provenance.v1+json")
	if err != nil || len(prov) != 1 || prov[0].ArtifactType != "application/vnd.vmafx.provenance.v1+json" {
		t.Fatalf("filtered referrers = %+v, %v", prov, err)
	}
	all, err := c.Referrers(ctx, host+"/r:model", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all referrers = %+v, %v", all, err)
	}
	if _, err = artifactClient(t, registry.Options{MaxReferrers: 1}, nil).Referrers(ctx, host+"/r:model", ""); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("over cap err = %v, want ErrTooLarge", err)
	}
}

func TestReferrers_API(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil, regsrv.WithReferrersSupport(true))
	c := artifactClient(t, registry.Options{}, nil)
	ctx := testCtx(t)
	subject, err := c.PushArtifact(ctx, host+"/r:model", registry.Artifact{ArtifactType: modelType})
	if err != nil {
		t.Fatalf("push subject: %v", err)
	}
	ref, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: scoreType, Subject: &subject})
	if err != nil {
		t.Fatalf("push referrer: %v", err)
	}
	all, err := c.Referrers(ctx, host+"/r:model", "")
	if err != nil || len(all) != 1 || all[0].Digest != ref.Digest {
		t.Fatalf("referrers via API = %+v, %v", all, err)
	}
}

// rawTaggable puts pre-serialized manifest bytes, the way cosign does.
type rawTaggable []byte

func (r rawTaggable) RawManifest() ([]byte, error)        { return r, nil }
func (r rawTaggable) MediaType() (types.MediaType, error) { return types.OCIManifestSchema1, nil }

// writeCosignBundle attaches a bundle referrer with the exact manifest
// layout `cosign sign --new-bundle-format` writes, using go-containerregistry
// directly so the producer is independent of the client under test.
func writeCosignBundle(t *testing.T, repo name.Repository, subject v1.Descriptor, bundle []byte) v1.Hash {
	t.Helper()
	cfg := static.NewLayer([]byte("{}"), "application/vnd.oci.empty.v1+json")
	layer := static.NewLayer(bundle, bundleType)
	for _, l := range []v1.Layer{cfg, layer} {
		if err := remote.WriteLayer(repo, l); err != nil {
			t.Fatalf("write layer: %v", err)
		}
	}
	desc := func(l v1.Layer, mt types.MediaType, artifactType string) map[string]any {
		d, _ := l.Digest()
		n, _ := l.Size()
		m := map[string]any{"mediaType": mt, "digest": d.String(), "size": n}
		if artifactType != "" {
			m["artifactType"] = artifactType
		}
		return m
	}
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     types.OCIManifestSchema1,
		"config":        desc(cfg, "application/vnd.oci.empty.v1+json", bundleType),
		"layers":        []any{desc(layer, bundleType, "")},
		"subject":       map[string]any{"mediaType": subject.MediaType, "digest": subject.Digest.String(), "size": subject.Size},
		"annotations": map[string]string{
			"org.opencontainers.image.created":  "2026-10-07T00:00:00Z",
			"dev.sigstore.bundle.content":       "dsse-envelope",
			"dev.sigstore.bundle.predicateType": "https://sigstore.dev/cosign/sign/v1",
		},
		"artifactType": bundleType,
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	h, _, err := v1.SHA256(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("hash manifest: %v", err)
	}
	if err = remote.Put(repo.Digest(h.String()), rawTaggable(raw)); err != nil {
		t.Fatalf("put bundle manifest: %v", err)
	}
	return h
}

// TestReferrers_CosignBundle proves a cosign bundle attached to a pushed
// artifact is discoverable by artifactType and readable through the client.
func TestReferrers_CosignBundle(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	ctx := testCtx(t)
	subject, err := c.PushArtifact(ctx, host+"/vmafx/model:v3", registry.Artifact{
		ArtifactType: modelType,
		Blobs:        []registry.Blob{{Name: "model.onnx", Reader: bytes.NewReader([]byte("weights"))}},
	})
	if err != nil {
		t.Fatalf("push model: %v", err)
	}
	repo, err := name.NewRepository(host + "/vmafx/model")
	if err != nil {
		t.Fatal(err)
	}
	bundle := []byte(`{"mediaType":"` + bundleType + `","dsseEnvelope":{}}`)
	bundleDigest := writeCosignBundle(t, repo, subject, bundle)

	refs, err := c.Referrers(ctx, host+"/vmafx/model:v3", bundleType)
	if err != nil || len(refs) != 1 || refs[0].Digest != bundleDigest {
		t.Fatalf("bundle referrers = %+v, %v; want %s", refs, err, bundleDigest)
	}
	if refs[0].Annotations["dev.sigstore.bundle.predicateType"] != "https://sigstore.dev/cosign/sign/v1" {
		t.Fatalf("referrer annotations = %v", refs[0].Annotations)
	}
	_, man, err := c.ArtifactManifest(ctx, host+"/vmafx/model@"+bundleDigest.String())
	if err != nil || len(man.Layers) != 1 || man.Layers[0].MediaType != bundleType || man.Subject.Digest != subject.Digest {
		t.Fatalf("bundle manifest = %+v, %v", man, err)
	}
	got, err := c.FetchBlob(ctx, host+"/vmafx/model", man.Layers[0], 1<<20)
	if err != nil || !bytes.Equal(got, bundle) {
		t.Fatalf("bundle bytes = %q, %v", got, err)
	}
	if others, _ := c.Referrers(ctx, host+"/vmafx/model:v3", scoreType); len(others) != 0 {
		t.Fatalf("artifactType filter leaked %v", others)
	}
}
