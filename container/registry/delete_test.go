// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/golusoris/golusoris/container/registry"
)

// present reports whether repo holds manifest h. The ggcr test registry
// keeps a tag after its manifest is deleted by digest, so tests look up
// digests.
func present(t *testing.T, c *registry.Client, repo string, h v1.Hash) bool {
	t.Helper()
	_, err := c.Resolve(testCtx(t), repo+"@"+h.String())
	return err == nil
}

func tagPresent(t *testing.T, c *registry.Client, repo, tag string) bool {
	t.Helper()
	_, err := c.Resolve(testCtx(t), repo+":"+tag)
	return err == nil
}

func describe(t *testing.T, c *registry.Client, ref string) v1.Descriptor {
	t.Helper()
	m, err := c.Manifest(testCtx(t), ref)
	if err != nil {
		t.Fatalf("manifest %s: %v", ref, err)
	}
	return v1.Descriptor{MediaType: m.MediaType, Digest: m.Digest, Size: m.Size}
}

// signedTree pushes a model, a cosign bundle referring to it and a report
// referring to the bundle; it returns them in delete order.
func signedTree(t *testing.T, c *registry.Client, host string) (model, bundle, report v1.Descriptor) {
	t.Helper()
	ctx := testCtx(t)
	model, err := c.PushArtifact(ctx, host+"/r:model", registry.Artifact{
		ArtifactType: modelType, Blobs: []registry.Blob{{Name: "model.onnx", Reader: strings.NewReader("weights")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := name.NewRepository(host + "/r")
	if err != nil {
		t.Fatal(err)
	}
	h := writeCosignBundle(t, repo, model, []byte(`{"mediaType":"`+bundleType+`"}`))
	bundle = describe(t, c, host+"/r@"+h.String())
	report, err = c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: scoreType, Subject: &bundle})
	if err != nil {
		t.Fatal(err)
	}
	return model, bundle, report
}

func hashes(ds ...v1.Descriptor) []v1.Hash {
	out := make([]v1.Hash, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Digest)
	}
	return out
}

func tagOf(h v1.Hash) string { return "sha256-" + h.Hex }

func TestDelete_ReferrerTree(t *testing.T) {
	t.Parallel()
	for _, api := range []bool{false, true} {
		t.Run(map[bool]string{false: "tag schema", true: "referrers api"}[api], func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			host := newArtifactRegistry(t, nil, regsrv.WithReferrersSupport(api))
			c := artifactClient(t, registry.Options{}, nil)
			model, bundle, report := signedTree(t, c, host)
			other, err := c.PushArtifact(ctx, host+"/r:other", registry.Artifact{ArtifactType: modelType, Annotations: map[string]string{"k": "other"}})
			if err != nil {
				t.Fatal(err)
			}
			otherSig, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: sigType, Subject: &other})
			if err != nil {
				t.Fatal(err)
			}
			want := registry.DeleteReport{Removed: hashes(report, bundle, model)}
			if !api {
				want.Tags = []string{tagOf(bundle.Digest), tagOf(model.Digest)}
			}
			ref := host + "/r@" + model.Digest.String()
			dry, err := c.Delete(ctx, ref, registry.DeleteOptions{DryRun: true})
			if err != nil || !equalReports(dry, want) {
				t.Fatalf("dry run = %+v, %v; want %+v", dry, err, want)
			}
			for _, d := range []v1.Descriptor{model, bundle, report} {
				if !present(t, c, host+"/r", d.Digest) {
					t.Fatalf("dry run deleted %s", d.Digest)
				}
			}
			got, err := c.Delete(ctx, ref, registry.DeleteOptions{})
			if err != nil || !equalReports(got, want) {
				t.Fatalf("Delete = %+v, %v; want %+v", got, err, want)
			}
			for _, d := range []v1.Descriptor{model, bundle, report} {
				if present(t, c, host+"/r", d.Digest) {
					t.Fatalf("%s survived", d.Digest)
				}
			}
			for _, h := range []v1.Hash{model.Digest, bundle.Digest} {
				if tagPresent(t, c, host+"/r", tagOf(h)) {
					t.Fatalf("referrers tag of %s survived", h)
				}
			}
			if !present(t, c, host+"/r", other.Digest) || !present(t, c, host+"/r", otherSig.Digest) {
				t.Fatal("unrelated artifact deleted")
			}
			if refs, rerr := c.Referrers(ctx, host+"/r@"+other.Digest.String(), ""); rerr != nil || len(refs) != 1 {
				t.Fatalf("unrelated referrers = %v, %v", refs, rerr)
			}
			again, err := c.Delete(ctx, ref, registry.DeleteOptions{})
			if err != nil || !equalReports(again, registry.DeleteReport{Absent: []v1.Hash{model.Digest}}) {
				t.Fatalf("second Delete = %+v, %v", again, err)
			}
		})
	}
}

func equalReports(a, b registry.DeleteReport) bool {
	return slices.Equal(a.Removed, b.Removed) && slices.Equal(a.Absent, b.Absent) && slices.Equal(a.Tags, b.Tags)
}

// TestDelete_PartialPriorDeletion resumes deletes an earlier run or another
// client left half done.
func TestDelete_PartialPriorDeletion(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	host := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	raw := func(h v1.Hash) {
		t.Helper()
		ref, err := name.NewDigest(host + "/r@" + h.String())
		if err != nil {
			t.Fatal(err)
		}
		if err = remote.Delete(ref, remote.WithTransport(newTestTransport(t))); err != nil {
			t.Fatal(err)
		}
	}
	// Subject gone, referrers left.
	model, bundle, report := signedTree(t, c, host)
	raw(model.Digest)
	got, err := c.Delete(ctx, host+"/r@"+model.Digest.String(), registry.DeleteOptions{})
	want := registry.DeleteReport{Removed: hashes(report, bundle), Absent: []v1.Hash{model.Digest}, Tags: []string{tagOf(bundle.Digest), tagOf(model.Digest)}}
	if err != nil || !equalReports(got, want) {
		t.Fatalf("subject gone: Delete = %+v, %v; want %+v", got, err, want)
	}
	// A referrer gone, still listed in the tag schema index.
	subject, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: modelType, Annotations: map[string]string{"k": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: sigType, Subject: &subject})
	if err != nil {
		t.Fatal(err)
	}
	raw(sig.Digest)
	got, err = c.Delete(ctx, host+"/r@"+subject.Digest.String(), registry.DeleteOptions{})
	want = registry.DeleteReport{Removed: []v1.Hash{subject.Digest}, Absent: []v1.Hash{sig.Digest}, Tags: []string{tagOf(subject.Digest)}}
	if err != nil || !equalReports(got, want) {
		t.Fatalf("referrer gone: Delete = %+v, %v; want %+v", got, err, want)
	}
}

// refuseDeletes answers every DELETE matching match with status and code,
// while on is set.
func refuseDeletes(on *atomic.Bool, match func(*http.Request) bool, status int, code string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete || !on.Load() || !match(r) {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": code, "message": "refused"}}})
		})
	}
}

func anyRequest(*http.Request) bool { return true }

func TestDelete_Unsupported(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status int
		code   string
	}{{http.StatusMethodNotAllowed, "METHOD_UNKNOWN"}, {http.StatusBadRequest, "METHOD_UNKNOWN"}, {http.StatusForbidden, "UNSUPPORTED"}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			var on atomic.Bool
			host := newArtifactRegistry(t, refuseDeletes(&on, anyRequest, tc.status, tc.code))
			c := artifactClient(t, registry.Options{}, nil)
			model, bundle, report := signedTree(t, c, host)
			on.Store(true)
			got, err := c.Delete(ctx, host+"/r@"+model.Digest.String(), registry.DeleteOptions{})
			if !errors.Is(err, registry.ErrDeleteUnsupported) || errors.Is(err, registry.ErrPartialDelete) {
				t.Fatalf("Delete err = %v, want ErrDeleteUnsupported only", err)
			}
			if !equalReports(got, registry.DeleteReport{}) {
				t.Fatalf("report = %+v, want empty", got)
			}
			for _, d := range []v1.Descriptor{model, bundle, report} {
				if !present(t, c, host+"/r", d.Digest) {
					t.Fatalf("%s deleted by a refused delete", d.Digest)
				}
			}
		})
	}
}

// TestDelete_PartialFailure refuses the subject's referrers tag or the
// subject itself after its referrers are gone: the report says what is gone
// and a retry finishes the job.
func TestDelete_PartialFailure(t *testing.T) {
	t.Parallel()
	for _, refuseTag := range []bool{false, true} {
		t.Run(map[bool]string{false: "subject refused", true: "tag refused"}[refuseTag], func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			var on atomic.Bool
			var refused atomic.Value
			refused.Store("")
			match := func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, refused.Load().(string)) }
			host := newArtifactRegistry(t, refuseDeletes(&on, match, http.StatusForbidden, "DENIED"))
			c := artifactClient(t, registry.Options{}, nil)
			model, bundle, report := signedTree(t, c, host)
			want := registry.DeleteReport{Removed: hashes(report, bundle), Tags: []string{tagOf(bundle.Digest), tagOf(model.Digest)}}
			retry := registry.DeleteReport{Removed: []v1.Hash{model.Digest}}
			refused.Store("/manifests/" + model.Digest.String())
			if refuseTag {
				refused.Store("/manifests/" + tagOf(model.Digest))
				want.Tags = want.Tags[:1]
				// The kept tag schema index still lists the deleted bundle.
				retry.Absent, retry.Tags = []v1.Hash{bundle.Digest}, []string{tagOf(model.Digest)}
			}
			on.Store(true)
			ref := host + "/r@" + model.Digest.String()
			got, err := c.Delete(ctx, ref, registry.DeleteOptions{})
			if !errors.Is(err, registry.ErrPartialDelete) || errors.Is(err, registry.ErrDeleteUnsupported) || !equalReports(got, want) {
				t.Fatalf("Delete = %+v, %v; want %+v with ErrPartialDelete", got, err, want)
			}
			if !present(t, c, host+"/r", model.Digest) {
				t.Fatal("subject deleted despite the refusal")
			}
			on.Store(false)
			got, err = c.Delete(ctx, ref, registry.DeleteOptions{})
			if err != nil || !equalReports(got, retry) {
				t.Fatalf("retry = %+v, %v; want %+v", got, err, retry)
			}
		})
	}
}

// TestDelete_TagDeleteFallsBackToDigest deletes a referrers tag schema index
// by digest on a registry that deletes manifests by digest only.
func TestDelete_TagDeleteFallsBackToDigest(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	var on atomic.Bool
	var mu sync.Mutex
	var deleted []string
	byTag := func(r *http.Request) bool {
		mu.Lock()
		deleted = append(deleted, r.URL.Path)
		mu.Unlock()
		return !strings.Contains(r.URL.Path[strings.LastIndex(r.URL.Path, "/"):], ":")
	}
	host := newArtifactRegistry(t, refuseDeletes(&on, byTag, http.StatusMethodNotAllowed, "UNSUPPORTED"))
	c := artifactClient(t, registry.Options{}, nil)
	model, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: modelType})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: sigType, Subject: &model}); err != nil {
		t.Fatal(err)
	}
	index := describe(t, c, host+"/r:"+tagOf(model.Digest))
	on.Store(true)
	got, err := c.Delete(ctx, host+"/r@"+model.Digest.String(), registry.DeleteOptions{})
	if err != nil || !slices.Equal(got.Tags, []string{tagOf(model.Digest)}) {
		t.Fatalf("Delete = %+v, %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(deleted, "/v2/r/manifests/"+index.Digest.String()) {
		t.Fatalf("referrers index not deleted by digest; DELETEs: %v", deleted)
	}
}

// rawIndex puts pre-serialized image index bytes.
type rawIndex []byte

func (r rawIndex) RawManifest() ([]byte, error)        { return r, nil }
func (r rawIndex) MediaType() (types.MediaType, error) { return types.OCIImageIndex, nil }

// TestDelete_RefusesForgedTagIndex refuses a tag schema index that lists a
// manifest which does not refer to the subject.
func TestDelete_RefusesForgedTagIndex(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	host := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	model, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: modelType})
	if err != nil {
		t.Fatal(err)
	}
	victim, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: scoreType})
	if err != nil {
		t.Fatal(err)
	}
	forged, err := json.Marshal(v1.IndexManifest{SchemaVersion: 2, MediaType: types.OCIImageIndex, Manifests: []v1.Descriptor{victim}})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag(host + "/r:" + tagOf(model.Digest))
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.Put(tag, rawIndex(forged), remote.WithTransport(newTestTransport(t))); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Delete(ctx, host+"/r@"+model.Digest.String(), registry.DeleteOptions{}); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Fatalf("Delete err = %v, want ErrInvalidArtifact", err)
	}
	if !present(t, c, host+"/r", victim.Digest) || !present(t, c, host+"/r", model.Digest) {
		t.Fatal("forged referrers list deleted a manifest")
	}
}

// TestDelete_IndexKeepsChildren deletes an index with its referrer; child
// manifests and their referrers stay.
func TestDelete_IndexKeepsChildren(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	host := newArtifactRegistry(t, nil, regsrv.WithReferrersSupport(true))
	c := artifactClient(t, registry.Options{}, nil)
	root, child, refs := seedSigned(t, c, host, false)
	got, err := c.Delete(ctx, host+"/app@"+root.Digest.String(), registry.DeleteOptions{})
	if err != nil || !equalReports(got, registry.DeleteReport{Removed: []v1.Hash{refs[0], root.Digest}}) {
		t.Fatalf("Delete = %+v, %v", got, err)
	}
	if !present(t, c, host+"/app", child.Digest) {
		t.Fatal("child manifest deleted")
	}
	if left, rerr := c.Referrers(ctx, host+"/app@"+child.Digest.String(), ""); rerr != nil || len(left) != 1 || left[0].Digest != refs[1] {
		t.Fatalf("child referrers = %v, %v", left, rerr)
	}
}

func TestDelete_ReferrersCap(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	host := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	model, bundle, report := signedTree(t, c, host)
	if _, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: scoreType, Subject: &model}); err != nil {
		t.Fatal(err)
	}
	ref := host + "/r@" + model.Digest.String()
	// Three referrers in the tree, at most two per listing.
	if got, err := artifactClient(t, registry.Options{MaxReferrers: 3}, nil).Delete(ctx, ref, registry.DeleteOptions{DryRun: true}); err != nil || len(got.Removed) != 4 {
		t.Fatalf("at cap: %+v, %v", got, err)
	}
	if _, err := artifactClient(t, registry.Options{MaxReferrers: 2}, nil).Delete(ctx, ref, registry.DeleteOptions{}); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("over cap err = %v, want ErrTooLarge", err)
	}
	for _, d := range []v1.Descriptor{model, bundle, report} {
		if !present(t, c, host+"/r", d.Digest) {
			t.Fatalf("%s deleted by a refused delete", d.Digest)
		}
	}
}

func TestDelete_InvalidInputs(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	c := artifactClient(t, registry.Options{}, nil)
	for _, ref := range []string{"registry.example/app:v1", "registry.example/app"} {
		if _, err := c.Delete(ctx, ref, registry.DeleteOptions{}); !errors.Is(err, registry.ErrInvalidArtifact) {
			t.Errorf("Delete(%q) err = %v, want ErrInvalidArtifact", ref, err)
		}
	}
	if _, err := c.Delete(ctx, "UPPER/Case::", registry.DeleteOptions{}); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Errorf("Delete of a malformed reference err = %v, want ErrInvalidArtifact", err)
	}
}

// referrersMode makes the referrers API answer 404 (mode 1: pushes write the
// tag schema) or an empty list (mode 2: an API that never learned them).
func referrersMode(mode *atomic.Int32) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Path, "/referrers/") || mode.Load() == 0 {
				next.ServeHTTP(w, r)
				return
			}
			if mode.Load() == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", string(types.OCIImageIndex))
			_, _ = w.Write([]byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`))
		})
	}
}

// TestDelete_UnionsAPIAndTagSchema finds referrers a registry recorded in the
// tag schema before it gained the referrers API.
func TestDelete_UnionsAPIAndTagSchema(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	var mode atomic.Int32
	mode.Store(1)
	host := newArtifactRegistry(t, referrersMode(&mode), regsrv.WithReferrersSupport(true))
	c := artifactClient(t, registry.Options{}, nil)
	model, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: modelType})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := c.PushArtifact(ctx, host+"/r", registry.Artifact{ArtifactType: sigType, Subject: &model})
	if err != nil {
		t.Fatal(err)
	}
	mode.Store(2)
	if refs, rerr := c.Referrers(ctx, host+"/r@"+model.Digest.String(), ""); rerr != nil || len(refs) != 0 {
		t.Fatalf("referrers API = %v, %v; want empty", refs, rerr)
	}
	got, err := c.Delete(ctx, host+"/r@"+model.Digest.String(), registry.DeleteOptions{})
	want := registry.DeleteReport{Removed: []v1.Hash{sig.Digest, model.Digest}, Tags: []string{tagOf(model.Digest)}}
	if err != nil || !equalReports(got, want) {
		t.Fatalf("Delete = %+v, %v; want %+v", got, err, want)
	}
}
