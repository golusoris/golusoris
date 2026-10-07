// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package trainerio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/internal/fileuri"
	"github.com/golusoris/golusoris/storage"
)

func TestCappedLogRetainsBoundAndReportsFullWrites(t *testing.T) {
	t.Parallel()
	log := NewCappedLog(8)
	written, err := log.Write([]byte("0123456789abcdef"))
	if err != nil || written != 16 {
		t.Fatalf("Write = %d, %v; want 16, nil", written, err)
	}
	if got := string(log.Bytes()); got != "01234567" {
		t.Fatalf("retained log = %q", got)
	}
	if !log.Truncated() {
		t.Fatal("discarded log bytes not reported")
	}
}

func TestReadOutputMaxIntLimitDoesNotOverflow(t *testing.T) {
	t.Parallel()
	output := t.TempDir()
	if err := os.WriteFile(filepath.Join(output, "artifact"), []byte("bounded"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadOutput(context.Background(), output, "artifact", math.MaxInt64)
	if err != nil {
		t.Fatalf("ReadOutput: %v", err)
	}
	if got := string(data); got != "bounded" {
		t.Fatalf("data = %q; want bounded", got)
	}
}

func TestStageCleansWorkDirectoryAfterConfigFailure(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	_, _, _, err := Stage("trainer-stage-*", map[string]any{"invalid": func() {}})
	if err == nil {
		t.Fatal("Stage accepted an unencodable config")
	}
	entries, readErr := os.ReadDir(tempRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed stage leaked %d work directories", len(entries))
	}
}

func TestStageRejectsOversizedConfigAndCleansWorkDirectory(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	_, _, _, err := Stage(
		"trainer-stage-*",
		map[string]any{"payload": strings.Repeat("x", int(DefaultMaxConfigBytes)-13)},
	)
	if err == nil || !strings.Contains(err.Error(), "config exceeds") {
		t.Fatalf("Stage oversized config error = %v", err)
	}
	entries, readErr := os.ReadDir(tempRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("oversized stage leaked %d work directories", len(entries))
	}
}

func TestStageAcceptsConfigAtExactByteLimit(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	workDir, inputDir, _, err := Stage(
		"trainer-stage-*",
		map[string]any{"payload": strings.Repeat("x", int(DefaultMaxConfigBytes)-14)},
	)
	if err != nil {
		t.Fatalf("Stage exact-limit config: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := Cleanup(workDir); cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	config, err := os.ReadFile(filepath.Join(inputDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(config)) != DefaultMaxConfigBytes {
		t.Fatalf("config size = %d; want %d", len(config), DefaultMaxConfigBytes)
	}
}

func TestCanonicalDatasetRootRejectsSymlinkAndRelativeRoots(t *testing.T) {
	t.Parallel()
	if _, err := CanonicalDatasetRoot("relative"); err == nil {
		t.Fatal("relative DatasetRoot accepted")
	}
	target := canonicalTempDir(t)
	link := filepath.Join(canonicalTempDir(t), "dataset-root")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := CanonicalDatasetRoot(link); err == nil {
		t.Fatal("symlinked DatasetRoot accepted")
	}
}

func TestStageDatasetCopiesLocalFileIntoContainerContract(t *testing.T) {
	t.Parallel()
	datasetRoot := canonicalTempDir(t)
	tenantRoot := filepath.Join(datasetRoot, "tenant-a")
	if err := os.Mkdir(tenantRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(tenantRoot, "dataset.jsonl")
	if err := os.WriteFile(source, []byte("{\"prompt\":\"p\",\"response\":\"r\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rawURI := fileURI(t, source)
	workDir, inputDir, outputDir, err := StageDataset(
		context.Background(), "trainer-dataset-*",
		map[string]any{"dataset_uri": rawURI}, datasetRoot, "tenant-a", rawURI, 64,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := Cleanup(workDir); cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	data, err := os.ReadFile(filepath.Join(inputDir, "dataset"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"response":"r"`) {
		t.Fatalf("staged dataset = %q", data)
	}
	configBytes, err := os.ReadFile(filepath.Join(inputDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	if config["dataset_uri"] != "file:///work/input/dataset" {
		t.Fatalf("dataset_uri = %v", config["dataset_uri"])
	}
	if runtime.GOOS != "windows" {
		inputInfo, statErr := os.Stat(inputDir)
		if statErr != nil {
			t.Fatal(statErr)
		}
		outputInfo, statErr := os.Stat(outputDir)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := inputInfo.Mode().Perm(); got != 0o555 {
			t.Fatalf("input mode = %o; want 555", got)
		}
		if got := outputInfo.Mode().Perm(); got != 0o777 {
			t.Fatalf("output mode = %o; want 777", got)
		}
	}
}

func TestStageDatasetRejectsInvalidAndOversizedSources(t *testing.T) {
	t.Parallel()
	datasetRoot := canonicalTempDir(t)
	if err := os.Mkdir(filepath.Join(datasetRoot, "tenant-a"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, rawURI := range []string{
		"relative.jsonl",
		"http://example.invalid/data.jsonl",
		"ftp://example.invalid/data.jsonl",
		"https://example.invalid/data.jsonl",
		"s3://bucket/data.jsonl",
		"https://example.invalid/data.jsonl?token=secret",
		"file://remotehost/tmp/data.jsonl",
	} {
		_, _, _, err := StageDataset(
			context.Background(), "trainer-dataset-*",
			map[string]any{"dataset_uri": rawURI}, datasetRoot, "tenant-a", rawURI, 4,
		)
		if err == nil {
			t.Fatalf("StageDataset accepted %q", rawURI)
		}
	}
	source := filepath.Join(datasetRoot, "tenant-a", "dataset")
	if err := os.WriteFile(source, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	rawURI := fileURI(t, source)
	_, _, _, err := StageDataset(
		context.Background(), "trainer-dataset-*",
		map[string]any{"dataset_uri": rawURI}, datasetRoot, "tenant-a", rawURI, 4,
	)
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("oversized error = %v; want ErrOutputTooLarge", err)
	}
}

func TestStageDatasetRejectsSymlinkSource(t *testing.T) {
	t.Parallel()
	datasetRoot := canonicalTempDir(t)
	tenantRoot := filepath.Join(datasetRoot, "tenant-a")
	if err := os.Mkdir(tenantRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tenantRoot, "target")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tenantRoot, "dataset")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rawURI := fileURI(t, link)
	_, _, _, err := StageDataset(
		context.Background(), "trainer-dataset-*",
		map[string]any{"dataset_uri": rawURI}, datasetRoot, "tenant-a", rawURI, 4,
	)
	if err == nil || !strings.Contains(err.Error(), "must not contain symbolic links") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestStageDatasetRejectsSymlinkTenantDirectory(t *testing.T) {
	t.Parallel()
	datasetRoot := canonicalTempDir(t)
	realTenant := filepath.Join(datasetRoot, "real-tenant")
	if err := os.Mkdir(realTenant, 0o700); err != nil {
		t.Fatal(err)
	}
	dataset := filepath.Join(realTenant, "dataset")
	if err := os.WriteFile(dataset, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realTenant, filepath.Join(datasetRoot, "tenant-a")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rawURI := fileURI(t, filepath.Join(datasetRoot, "tenant-a", "dataset"))
	_, _, _, err := StageDataset(
		context.Background(), "trainer-dataset-*", map[string]any{"dataset_uri": rawURI},
		datasetRoot, "tenant-a", rawURI, 16,
	)
	if err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
		t.Fatalf("tenant symlink error = %v", err)
	}
}

func TestStageDatasetRejectsTenantEscapeAndNonRegularSources(t *testing.T) {
	t.Parallel()
	datasetRoot := canonicalTempDir(t)
	tenantA := filepath.Join(datasetRoot, "tenant-a")
	tenantB := filepath.Join(datasetRoot, "tenant-b")
	for _, directory := range []string{tenantA, tenantB, filepath.Join(tenantA, "real")} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(tenantB, "secret")
	if err := os.WriteFile(secret, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	realDataset := filepath.Join(tenantA, "real", "dataset")
	if err := os.WriteFile(realDataset, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tenantA, "link")
	if err := os.Symlink(filepath.Join(tenantA, "real"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tests := []string{
		fileURI(t, secret),
		fileURI(t, tenantA) + "/%2e%2e/tenant-b/secret",
		fileURI(t, filepath.Join(link, "dataset")),
		fileURI(t, filepath.Join(tenantA, "real")),
	}
	for _, rawURI := range tests {
		_, _, _, err := StageDataset(
			context.Background(), "trainer-dataset-*", map[string]any{"dataset_uri": rawURI},
			datasetRoot, "tenant-a", rawURI, 16,
		)
		if err == nil {
			t.Fatalf("StageDataset accepted confined-path violation %q", rawURI)
		}
	}
	if mkfifo, err := exec.LookPath("mkfifo"); err == nil {
		fifo := filepath.Join(tenantA, "fifo")
		if output, commandErr := exec.Command(mkfifo, fifo).CombinedOutput(); commandErr != nil {
			t.Fatalf("mkfifo: %v: %s", commandErr, output)
		}
		rawURI := fileURI(t, fifo)
		_, _, _, stageErr := StageDataset(
			context.Background(), "trainer-dataset-*", map[string]any{"dataset_uri": rawURI},
			datasetRoot, "tenant-a", rawURI, 16,
		)
		if stageErr == nil {
			t.Fatal("StageDataset accepted FIFO")
		}
	}
}

func TestStageDatasetUsesDefaultTenantDirectoryAtExactCap(t *testing.T) {
	t.Parallel()
	datasetRoot := canonicalTempDir(t)
	defaultRoot := filepath.Join(datasetRoot, "_default")
	if err := os.Mkdir(defaultRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	dataset := filepath.Join(defaultRoot, "dataset")
	if err := os.WriteFile(dataset, []byte("1234"), 0o600); err != nil {
		t.Fatal(err)
	}
	rawURI := fileURI(t, dataset)
	workDir, _, _, err := StageDataset(
		context.Background(), "trainer-dataset-*", map[string]any{"dataset_uri": rawURI},
		datasetRoot, "", rawURI, 4,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = Cleanup(workDir); err != nil {
		t.Fatal(err)
	}
}

func fileURI(t *testing.T, path string) string {
	t.Helper()
	uri, err := fileuri.FromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return uri
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReadOutputRejectsOversizedAndSymlinkFiles(t *testing.T) {
	t.Parallel()
	output := t.TempDir()
	if err := os.WriteFile(filepath.Join(output, "artifact"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOutput(context.Background(), output, "artifact", 4); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("oversized error = %v; want ErrOutputTooLarge", err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte(strings.Repeat("s", 2)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(output, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ReadOutput(context.Background(), output, "link", 4); err == nil {
		t.Fatal("symlink output accepted")
	}
}

func TestArtifactKeyIsTenantAndContentIsolated(t *testing.T) {
	t.Parallel()
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	keyA, err := ArtifactKey("models/gemma", "tenant-a", "intent", "job-1", digestA, "adapter.lora.h5")
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := ArtifactKey("models/gemma", "tenant-b", "intent", "job-1", digestA, "adapter.lora.h5")
	if err != nil {
		t.Fatal(err)
	}
	retrained, err := ArtifactKey("models/gemma", "tenant-a", "intent", "job-1", digestB, "adapter.lora.h5")
	if err != nil {
		t.Fatal(err)
	}
	if keyA == keyB || keyA == retrained {
		t.Fatalf("artifact keys collide: %q %q %q", keyA, keyB, retrained)
	}
	if want := "models/gemma/tenants/tenant-a/intent/job-1/" + digestA + "/adapter.lora.h5"; keyA != want {
		t.Fatalf("key = %q; want %q", keyA, want)
	}
}

func TestArtifactKeyRejectsNamespaceEscapeAndBlankID(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	for _, test := range []struct {
		prefix, tenant, name, jobID string
	}{
		{"../models", "tenant", "name", "job"},
		{"models/gemma", "../tenant", "name", "job"},
		{"models/gemma", "tenant", "../name", "job"},
		{"models/gemma", "tenant", "name", ""},
		{"models/gemma", "tenant", "name", "../job"},
	} {
		if _, err := ArtifactKey(
			test.prefix, test.tenant, test.name, test.jobID, digest, "artifact.tflite",
		); err == nil {
			t.Fatalf("ArtifactKey accepted prefix=%q tenant=%q name=%q id=%q", test.prefix, test.tenant, test.name, test.jobID)
		}
	}
}

type mutatingBucket struct {
	storage.Bucket
	beforePut func()
}

func (b mutatingBucket) Put(
	ctx context.Context, key string, reader io.Reader, opts storage.PutOptions,
) (storage.Object, error) {
	b.beforePut()
	return b.Bucket.Put(ctx, key, reader, opts)
}

func TestUploadOutputSnapshotsBeforeSourceMutation(t *testing.T) {
	t.Parallel()
	output := t.TempDir()
	snapshotDir := t.TempDir()
	if err := os.Chmod(snapshotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(output, "artifact")
	if err := os.WriteFile(source, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	stored, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bucket := mutatingBucket{Bucket: stored, beforePut: func() {
		if writeErr := os.WriteFile(source, []byte("xyz"), 0o600); writeErr != nil {
			t.Errorf("mutate source: %v", writeErr)
		}
	}}
	object, err := UploadOutput(context.Background(), bucket, OutputUpload{
		OutputDir: output, SnapshotDir: snapshotDir, ArtifactName: "artifact",
		KeyPrefix: "models/test", TenantID: "tenant", ModelName: "model",
		JobID: "job", MaxBytes: 3, ContentType: storage.DefaultContentType,
	})
	if err != nil {
		t.Fatal(err)
	}
	const digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if !strings.Contains(object.Key, "/"+digest+"/artifact") {
		t.Fatalf("key = %q; want digest %s", object.Key, digest)
	}
	reader, _, err := stored.Get(context.Background(), object.Key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "abc" {
		t.Fatalf("uploaded body = %q; want snapshotted abc", got)
	}
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("snapshot directory retains %d files", len(entries))
	}
}

func TestValidateOpenedOutputRejectsDifferentSameSizeFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.WriteFile(first, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("xyz"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateOpenedOutput("artifact", firstInfo, secondInfo, 3); err == nil {
		t.Fatal("different same-size output identity accepted")
	}
	if err = validateOpenedOutput("artifact", firstInfo, firstInfo, 3); err != nil {
		t.Fatalf("same output identity rejected: %v", err)
	}
}

func TestUploadOutputRejectsSnapshotInsideWritableOutput(t *testing.T) {
	t.Parallel()
	output := t.TempDir()
	if err := os.Chmod(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "artifact"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	bucket, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = UploadOutput(context.Background(), bucket, OutputUpload{
		OutputDir: output, SnapshotDir: output, ArtifactName: "artifact",
		KeyPrefix: "models/test", TenantID: "tenant", ModelName: "model",
		JobID: "job", MaxBytes: 3, ContentType: storage.DefaultContentType,
	})
	if err == nil || !strings.Contains(err.Error(), "outside the writable output") {
		t.Fatalf("unsafe snapshot directory error = %v", err)
	}
}
