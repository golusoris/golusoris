// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func setLocalParentSync(bucket *LocalBucket, sync func(root *os.Root, parent string) error) {
	bucket.syncParent = &localParentSyncer{run: sync}
}

func TestLocalBucket_PutSyncsPublishedParent(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	bucket, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	setLocalParentSync(bucket, func(root *os.Root, parent string) error {
		called = true
		if parent != "nested" {
			t.Fatalf("sync parent = %q; want nested", parent)
		}
		if _, statErr := root.Stat("nested/object"); statErr != nil {
			t.Fatalf("published object not visible before parent sync: %v", statErr)
		}
		return nil
	})

	if _, err = bucket.Put(context.Background(), "nested/object", strings.NewReader("new"), PutOptions{}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !called {
		t.Fatal("Put did not sync the published object's parent")
	}
}

func TestLocalBucket_PutSyncsNewDirectoryAncestorsBottomUp(t *testing.T) {
	t.Parallel()
	bucket, err := NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var synced []string
	setLocalParentSync(bucket, func(root *os.Root, parent string) error {
		if _, statErr := root.Stat(filepath.Join("a", "b", "object")); statErr != nil {
			t.Fatalf("published object not visible before %q sync: %v", parent, statErr)
		}
		synced = append(synced, parent)
		return nil
	})

	if _, err = bucket.Put(context.Background(), "a/b/object", strings.NewReader("new"), PutOptions{}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := []string{filepath.Join("a", "b"), "a", "."}
	if !slices.Equal(synced, want) {
		t.Fatalf("synced directories = %q; want %q", synced, want)
	}
}

func TestLocalBucket_NewAncestorSyncFailureIsIndeterminateCommit(t *testing.T) {
	t.Parallel()
	bucket, err := NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected new-ancestor sync failure")
	setLocalParentSync(bucket, func(_ *os.Root, parent string) error {
		if parent == "a" {
			return wantErr
		}
		return nil
	})

	_, err = bucket.Put(context.Background(), "a/b/object", strings.NewReader("new"), PutOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Put error = %v; want injected ancestor sync error", err)
	}
	exists, existsErr := bucket.Exists(context.Background(), "a/b/object")
	if existsErr != nil || !exists {
		t.Fatalf("object after indeterminate Put: exists=%t err=%v", exists, existsErr)
	}
}

func TestLocalBucket_ParentSyncFailureIsIndeterminateCommit(t *testing.T) {
	t.Parallel()
	bucket, err := NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bucket.Put(context.Background(), "object", strings.NewReader("old"), PutOptions{}); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	wantErr := errors.New("injected parent sync failure")
	setLocalParentSync(bucket, func(*os.Root, string) error { return wantErr })

	if _, err = bucket.Put(context.Background(), "object", strings.NewReader("new"), PutOptions{}); !errors.Is(err, wantErr) {
		t.Fatalf("replacement Put error = %v; want injected sync error", err)
	}
	rc, _, err := bucket.Get(context.Background(), "object")
	if err != nil {
		t.Fatalf("Get published replacement: %v", err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			t.Errorf("close published replacement: %v", closeErr)
		}
	}()
	data := make([]byte, 3)
	if _, err = rc.Read(data); err != nil {
		t.Fatalf("read published replacement: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("published replacement = %q; want new", data)
	}
}

func TestLocalBucket_DeleteSyncFailureIsIndeterminateCommit(t *testing.T) {
	t.Parallel()
	bucket, err := NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bucket.Put(context.Background(), "nested/object", strings.NewReader("data"), PutOptions{}); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	wantErr := errors.New("injected delete parent sync failure")
	setLocalParentSync(bucket, func(root *os.Root, parent string) error {
		if parent != "nested" {
			t.Fatalf("sync parent = %q; want nested", parent)
		}
		if _, statErr := root.Stat("nested/object"); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("object state during parent sync = %v; want absent", statErr)
		}
		return wantErr
	})
	if err = bucket.Delete(context.Background(), "nested/object"); !errors.Is(err, wantErr) {
		t.Fatalf("Delete error = %v; want injected sync error", err)
	}
	exists, err := bucket.Exists(context.Background(), "nested/object")
	if err != nil || exists {
		t.Fatalf("object after indeterminate delete: exists=%t err=%v", exists, err)
	}
}

func TestLocalBucket_MetadataSidecarLifecycleAndListHiding(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	bucket, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	const key = "nested/object"
	if _, err = bucket.Put(context.Background(), key, strings.NewReader("first"), PutOptions{
		ContentType: "text/plain", Metadata: map[string]string{"generation": "one"},
	}); err != nil {
		t.Fatalf("Put attributes: %v", err)
	}
	metadataName := localMetadataPath(key, filepath.FromSlash(key))
	metadataPath := filepath.Join(base, metadataName)
	info, err := os.Lstat(metadataPath)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("metadata sidecar = %v, %v; want regular file", info, err)
	}
	objects, err := bucket.List(context.Background(), ListOptions{Prefix: "nested/"})
	if err != nil || len(objects) != 1 || objects[0].Key != key {
		t.Fatalf("List = %+v, %v; want only %q", objects, err, key)
	}

	if _, err = bucket.Put(context.Background(), key, strings.NewReader("default"), PutOptions{}); err != nil {
		t.Fatalf("Put default replacement: %v", err)
	}
	info, err = os.Lstat(metadataPath)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("default replacement metadata state = %v, %v; want regular sidecar", info, err)
	}
	object, err := bucket.Stat(context.Background(), key)
	if err != nil || object.ContentType != DefaultContentType || object.Metadata != nil {
		t.Fatalf("default Stat = %+v, %v", object, err)
	}

	if _, err = bucket.Put(context.Background(), key, strings.NewReader("third"), PutOptions{
		ContentType: "text/third", Metadata: map[string]string{"generation": "three"},
	}); err != nil {
		t.Fatalf("Put attributes again: %v", err)
	}
	if err = bucket.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err = os.Lstat(metadataPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted metadata state = %v; want absent", err)
	}
	objects, err = bucket.List(context.Background(), ListOptions{Prefix: "nested/"})
	if err != nil || len(objects) != 0 {
		t.Fatalf("List after Delete = %+v, %v; want empty", objects, err)
	}
}

func TestLocalBucket_MetadataSidecarRejectsSymlinkWithoutFollowing(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	bucket, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	const key = "object"
	if _, err = bucket.Put(context.Background(), key, strings.NewReader("body"), PutOptions{
		ContentType: "text/plain",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	metadataPath := filepath.Join(base, localMetadataPath(key, key))
	if err = os.Remove(metadataPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "sentinel")
	if err = os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, metadataPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	reader, _, getErr := bucket.Get(context.Background(), key)
	if reader != nil {
		if closeErr := reader.Close(); closeErr != nil {
			t.Errorf("close unexpected reader: %v", closeErr)
		}
		t.Fatal("Get returned a reader for symlinked metadata")
	}
	if getErr == nil || !strings.Contains(getErr.Error(), "regular file") {
		t.Fatalf("Get error = %v; want regular-file rejection", getErr)
	}
	if _, statErr := bucket.Stat(context.Background(), key); statErr == nil ||
		!strings.Contains(statErr.Error(), "regular file") {
		t.Fatalf("Stat error = %v; want regular-file rejection", statErr)
	}
	if err = bucket.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("outside file = %q, %v; want unchanged", data, err)
	}
}

func TestLocalBucket_ObjectSymlinkToInRootFileIsRejected(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "target"), []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(base, "alias")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	bucket, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	reader, _, getErr := bucket.Get(context.Background(), "alias")
	if reader != nil {
		if closeErr := reader.Close(); closeErr != nil {
			t.Errorf("close unexpected symlink reader: %v", closeErr)
		}
		t.Fatal("Get returned a reader for an in-root object symlink")
	}
	assertLocalRegularFileError(t, "Get", getErr)
	_, statErr := bucket.Stat(context.Background(), "alias")
	assertLocalRegularFileError(t, "Stat", statErr)
	_, existsErr := bucket.Exists(context.Background(), "alias")
	assertLocalRegularFileError(t, "Exists", existsErr)
	_, urlErr := bucket.URL(context.Background(), "alias")
	assertLocalRegularFileError(t, "URL", urlErr)
}

func assertLocalRegularFileError(t *testing.T, operation string, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("%s error = %v; want regular-file rejection", operation, err)
	}
}

func TestLocalBucket_ConcurrentReplacementsKeepBodyAndMetadataPaired(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	firstBucket, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	secondBucket, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	for iteration := range 32 {
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for index, generation := range []string{"a", "b"} {
			bucket := firstBucket
			if index == 1 {
				bucket = secondBucket
			}
			workers.Add(1)
			go func(bucket *LocalBucket) {
				defer workers.Done()
				<-start
				_, putErr := bucket.Put(
					context.Background(), "object", strings.NewReader(generation),
					PutOptions{ContentType: "text/" + generation, Metadata: map[string]string{"generation": generation}},
				)
				results <- putErr
			}(bucket)
		}
		close(start)
		workers.Wait()
		close(results)
		for putErr := range results {
			if putErr != nil {
				t.Fatalf("iteration %d Put: %v", iteration, putErr)
			}
		}
		reader, object, getErr := firstBucket.Get(context.Background(), "object")
		if getErr != nil {
			t.Fatalf("iteration %d Get: %v", iteration, getErr)
		}
		body, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("iteration %d read/close: %v / %v", iteration, readErr, closeErr)
		}
		generation := string(body)
		if object.ContentType != "text/"+generation || object.Metadata["generation"] != generation {
			t.Fatalf("iteration %d pair = body %q, object %+v", iteration, body, object)
		}
	}
}

func TestLocalBucket_TwoInstancesShareOperationLock(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	first, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	root, err := first.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close lock test root: %v", closeErr)
		}
	}()
	lock, err := first.lockAndRecover(context.Background(), root, "test hold")
	if err != nil {
		t.Fatal(err)
	}
	// The waiter's own retry limit bounds the wait; the fsyncs after hand-off are host-paced.
	ctx, cancel := context.WithTimeout(context.Background(), maxLocalLockAttempts*localLockRetryInterval)
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, putErr := second.Put(ctx, "object", strings.NewReader("body"), PutOptions{})
		result <- putErr
	}()
	// Join the Put before TempDir cleanup; Windows cannot remove a staged file still open.
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	select {
	case putErr := <-result:
		t.Fatalf("second Put completed while first instance held lock: %v", putErr)
	case <-time.After(30 * time.Millisecond):
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if putErr := <-result; putErr != nil {
		t.Fatalf("second Put after shared lock release: %v", putErr)
	}
}

func TestLocalBucket_RecoversEveryInterruptedPublishPhase(t *testing.T) {
	t.Parallel()
	for phase := range 5 {
		t.Run(fmt.Sprintf("phase-%d", phase), func(t *testing.T) {
			t.Parallel()
			fixture := prepareInterruptedLocalReplacement(t)
			applyInterruptedLocalReplacementPhase(t, fixture.root, fixture.tx, phase)
			if err := fixture.lock.Close(); err != nil {
				t.Fatalf("release simulated crashed writer: %v", err)
			}
			if err := fixture.root.Close(); err != nil {
				t.Fatalf("close simulated crashed writer root: %v", err)
			}

			reopened, err := NewLocalBucket(fixture.base)
			if err != nil {
				t.Fatal(err)
			}
			reader, object, err := reopened.Get(context.Background(), "object")
			if err != nil {
				t.Fatalf("Get after recovery: %v", err)
			}
			body, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read recovered object: %v / %v", readErr, closeErr)
			}
			wantGeneration := "old"
			if phase == 4 {
				wantGeneration = "new"
			}
			if string(body) != wantGeneration || object.ContentType != "text/"+wantGeneration ||
				object.Metadata["generation"] != wantGeneration {
				t.Fatalf("recovered pair = body %q, object %+v; want %q", body, object, wantGeneration)
			}
			objects, err := reopened.List(context.Background(), ListOptions{})
			if err != nil || len(objects) != 1 || objects[0].Key != "object" {
				t.Fatalf("List after recovery = %+v, %v; want only object", objects, err)
			}
			assertNoTransientLocalFiles(t, fixture.base)
		})
	}
}

type interruptedLocalReplacement struct {
	base string
	root *os.Root
	lock *localOperationLock
	tx   localTransaction
}

func prepareInterruptedLocalReplacement(t *testing.T) interruptedLocalReplacement {
	t.Helper()
	base := t.TempDir()
	bucket, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bucket.Put(context.Background(), "object", strings.NewReader("old"), PutOptions{
		ContentType: "text/old", Metadata: map[string]string{"generation": "old"},
	}); err != nil {
		t.Fatalf("seed old pair: %v", err)
	}
	root, err := bucket.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := bucket.lockAndRecover(context.Background(), root, "prepare interrupted replacement")
	if err != nil {
		t.Fatal(err)
	}
	objectStage, size, digest, err := stageLocalObject(context.Background(), root, strings.NewReader("new"))
	if err != nil {
		t.Fatal(err)
	}
	attrs := snapshotLocalAttributes("object", PutOptions{
		ContentType: "text/new", Metadata: map[string]string{"generation": "new"},
	})
	_, encoded, err := bindLocalAttributes(attrs, size, digest)
	if err != nil {
		t.Fatal(err)
	}
	metadataStage, err := stageLocalAttributes(context.Background(), root, encoded)
	if err != nil {
		t.Fatal(err)
	}
	tx := newLocalTransaction(
		"object", "object", objectStage, metadataStage, size, digest, encoded, true, true,
	)
	started, err := beginLocalTransaction(context.Background(), root, tx)
	if err != nil || !started {
		t.Fatalf("begin transaction = %t, %v", started, err)
	}
	return interruptedLocalReplacement{base: base, root: root, lock: lock, tx: tx}
}

func applyInterruptedLocalReplacementPhase(
	t *testing.T, root *os.Root, tx localTransaction, phase int,
) {
	t.Helper()
	steps := []struct {
		from string
		to   string
	}{
		{tx.MetadataTarget, tx.MetadataBackup},
		{tx.MetadataStage, tx.MetadataTarget},
		{tx.ObjectTarget, tx.ObjectBackup},
		{tx.ObjectStage, tx.ObjectTarget},
	}
	for index, step := range steps {
		if index >= phase {
			return
		}
		if err := root.Rename(step.from, step.to); err != nil {
			t.Fatalf("apply interrupted phase %d step %d: %v", phase, index, err)
		}
	}
}

func assertNoTransientLocalFiles(t *testing.T, base string) {
	t.Helper()
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if isLocalTempName(name) || name == localTransactionName {
			t.Errorf("transient local file remains after recovery: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk recovered bucket: %v", err)
	}
}

func TestLocalOperationLock_CancellationDoesNotLeak(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	firstRoot, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := firstRoot.Close(); closeErr != nil {
			t.Errorf("close first lock root: %v", closeErr)
		}
	}()
	secondRoot, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := secondRoot.Close(); closeErr != nil {
			t.Errorf("close second lock root: %v", closeErr)
		}
	}()
	first, err := acquireLocalOperationLock(context.Background(), firstRoot, "first")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err = acquireLocalOperationLock(ctx, secondRoot, "contender"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended lock error = %v; want deadline exceeded", err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := acquireLocalOperationLock(context.Background(), secondRoot, "after cancellation")
	if err != nil {
		t.Fatalf("lock after canceled contender: %v", err)
	}
	if err = after.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalOperationLock_UsesOpenedRootIdentity(t *testing.T) {
	t.Parallel()
	container := t.TempDir()
	base := filepath.Join(container, "base")
	oldBase := filepath.Join(container, "old-base")
	if err := os.Mkdir(base, 0o750); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close identity root: %v", closeErr)
		}
	}()
	if err = os.Rename(base, oldBase); err != nil {
		t.Skipf("cannot rename opened root: %v", err)
	}
	if err = os.Mkdir(base, 0o750); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireLocalOperationLock(context.Background(), root, "opened-root identity")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			t.Errorf("close identity lock: %v", closeErr)
		}
	}()
	if info, statErr := os.Lstat(filepath.Join(oldBase, localLockName)); statErr != nil || !info.Mode().IsRegular() {
		t.Fatalf("opened-root lock = %v, %v; want regular file", info, statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(base, localLockName)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("replacement-root lock state = %v; want absent", statErr)
	}
}

func TestLocalBucket_ReclaimsPreJournalOrphanStages(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	bucket, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	root, err := bucket.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := bucket.lockAndRecover(context.Background(), root, "seed orphan")
	if err != nil {
		t.Fatal(err)
	}
	file, orphan, err := openLocalTemp(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("interrupted")); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	exists, err := reopened.Exists(context.Background(), "missing")
	if err != nil || exists {
		t.Fatalf("Exists after orphaned stage = %t, %v; want false, nil", exists, err)
	}
	if _, err = os.Lstat(filepath.Join(base, orphan)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("orphan stage state = %v; want absent", err)
	}
}

func TestReclaimLocalOrphanStages_PreservesJournalStages(t *testing.T) {
	t.Parallel()
	fixture := prepareInterruptedLocalReplacement(t)
	if err := reclaimLocalOrphanStages(context.Background(), fixture.root); err != nil {
		t.Fatalf("reclaim with active journal: %v", err)
	}
	for _, stage := range []string{fixture.tx.ObjectStage, fixture.tx.MetadataStage} {
		if info, err := fixture.root.Lstat(stage); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("protected stage %q = %v, %v; want regular file", stage, info, err)
		}
	}
	if err := recoverPendingLocalTransaction(context.Background(), fixture.root); err != nil {
		t.Fatalf("recover protected transaction: %v", err)
	}
	if err := fixture.lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.root.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReclaimLocalOrphanStages_RejectsUnsafeAndOversizedState(t *testing.T) {
	t.Parallel()
	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		bucket, root, lock := prepareLocalStageRecovery(t)
		defer closeLocalStageRecovery(t, root, lock)
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
			t.Fatal(err)
		}
		stage := filepath.Join(bucket.base, localStagingDirectoryName, localTempPrefix+"unsafe"+localTempSuffix)
		if err := os.Symlink(outside, stage); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := reclaimLocalOrphanStages(context.Background(), root); err == nil ||
			!strings.Contains(err.Error(), "regular file") {
			t.Fatalf("unsafe orphan error = %v; want regular-file rejection", err)
		}
		data, err := os.ReadFile(outside)
		if err != nil || string(data) != "unchanged" {
			t.Fatalf("outside file = %q, %v; want unchanged", data, err)
		}
	})
	t.Run("entry cap exact", func(t *testing.T) {
		t.Parallel()
		_, root, lock := prepareLocalStageRecovery(t)
		defer closeLocalStageRecovery(t, root, lock)
		stages := seedLocalOrphanStages(t, root, maxLocalOrphanStages)
		if err := reclaimLocalOrphanStages(context.Background(), root); err != nil {
			t.Fatalf("reclaim exact staging cap: %v", err)
		}
		for _, stage := range stages {
			if _, err := root.Lstat(stage); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("reclaimed stage %q state = %v; want absent", stage, err)
			}
		}
	})
	t.Run("entry cap", func(t *testing.T) {
		t.Parallel()
		_, root, lock := prepareLocalStageRecovery(t)
		defer closeLocalStageRecovery(t, root, lock)
		seedLocalOrphanStages(t, root, maxLocalOrphanStages+1)
		if err := reclaimLocalOrphanStages(context.Background(), root); err == nil ||
			!strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized staging error = %v; want cap rejection", err)
		}
	})
}

func seedLocalOrphanStages(t *testing.T, root *os.Root, count int) []string {
	t.Helper()
	stages := make([]string, 0, count)
	for index := range count {
		name := filepath.Join(
			localStagingDirectoryName,
			fmt.Sprintf("%scap-%02d%s", localTempPrefix, index, localTempSuffix),
		)
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		stages = append(stages, name)
	}
	return stages
}

func prepareLocalStageRecovery(t *testing.T) (*LocalBucket, *os.Root, *localOperationLock) {
	t.Helper()
	bucket, err := NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := bucket.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := bucket.lockAndRecover(context.Background(), root, "prepare orphan recovery")
	if err != nil {
		t.Fatal(err)
	}
	return bucket, root, lock
}

func closeLocalStageRecovery(t *testing.T, root *os.Root, lock *localOperationLock) {
	t.Helper()
	if err := lock.Close(); err != nil {
		t.Errorf("close orphan-recovery lock: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Errorf("close orphan-recovery root: %v", err)
	}
}

func TestLocalOperationLock_RejectsUnsafeNodesAndReplacement(t *testing.T) {
	t.Parallel()
	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		if err := os.Mkdir(filepath.Join(base, localLockName), 0o700); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := root.Close(); closeErr != nil {
				t.Errorf("close unsafe-directory root: %v", closeErr)
			}
		}()
		if _, err = acquireLocalOperationLock(context.Background(), root, "unsafe directory"); err == nil ||
			!strings.Contains(err.Error(), "regular file") {
			t.Fatalf("directory lock error = %v; want regular-file rejection", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(base, localLockName)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := root.Close(); closeErr != nil {
				t.Errorf("close unsafe-symlink root: %v", closeErr)
			}
		}()
		if _, err = acquireLocalOperationLock(context.Background(), root, "unsafe symlink"); err == nil ||
			!strings.Contains(err.Error(), "regular file") {
			t.Fatalf("symlink lock error = %v; want regular-file rejection", err)
		}
	})
	t.Run("replaced identity", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		path := filepath.Join(base, localLockName)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := root.Close(); closeErr != nil {
				t.Errorf("close replaced-identity root: %v", closeErr)
			}
		}()
		before, err := root.Lstat(localLockName)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err = openExistingLocalLock(root, before); err == nil ||
			!strings.Contains(err.Error(), "changed during open") {
			t.Fatalf("replaced lock error = %v; want identity rejection", err)
		}
	})
}

func TestLocalBucket_MetadataPublishFailureRollsBackOldPair(t *testing.T) {
	t.Parallel()
	fixture := prepareInterruptedLocalReplacement(t)
	if err := fixture.root.Remove(fixture.tx.MetadataStage); err != nil {
		t.Fatal(err)
	}
	publishErr := publishLocalTransaction(fixture.root, fixture.tx)
	if publishErr == nil {
		t.Fatal("publish with missing metadata stage succeeded")
	}
	if err := recoverPendingLocalTransaction(context.Background(), fixture.root); err != nil {
		t.Fatalf("recover failed publication: %v", err)
	}
	if err := fixture.lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.root.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewLocalBucket(fixture.base)
	if err != nil {
		t.Fatal(err)
	}
	reader, object, err := reopened.Get(context.Background(), "object")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read old pair: %v / %v", readErr, closeErr)
	}
	if string(body) != "old" || object.ContentType != "text/old" || object.Metadata["generation"] != "old" {
		t.Fatalf("pair after metadata failure = body %q object %+v", body, object)
	}
	assertNoTransientLocalFiles(t, fixture.base)
}

func TestLocalMetadataSizeBoundaryAndReservedNamespace(t *testing.T) {
	t.Parallel()
	attrs := snapshotLocalAttributes("object", PutOptions{Metadata: map[string]string{"value": ""}})
	_, baseline, err := bindLocalAttributes(attrs, 1, strings.Repeat("0", sha256.Size*2))
	if err != nil {
		t.Fatal(err)
	}
	exactValue := strings.Repeat("x", maxLocalAttributeBytes-len(baseline))
	attrs.Metadata["value"] = exactValue
	_, exact, err := bindLocalAttributes(attrs, 1, strings.Repeat("0", sha256.Size*2))
	if err != nil || len(exact) != maxLocalAttributeBytes {
		t.Fatalf("exact-boundary encode = %d bytes, %v", len(exact), err)
	}
	attrs.Metadata["value"] = exactValue + "x"
	if _, _, err = bindLocalAttributes(attrs, 1, strings.Repeat("0", sha256.Size*2)); err == nil {
		t.Fatal("metadata cap + 1 was accepted")
	}
	base := t.TempDir()
	bucket, err := NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bucket.Put(context.Background(), "object", strings.NewReader("x"), PutOptions{
		Metadata: map[string]string{"value": exactValue},
	}); err != nil {
		t.Fatalf("Put exact metadata boundary: %v", err)
	}
	if _, err = bucket.Put(context.Background(), "object", strings.NewReader("y"), PutOptions{
		Metadata: map[string]string{"value": exactValue + "x"},
	}); err == nil {
		t.Fatal("Put metadata boundary + 1 succeeded")
	}
	object, err := bucket.Stat(context.Background(), "object")
	if err != nil || object.Metadata["value"] != exactValue {
		t.Fatalf("object after rejected metadata + 1 = %+v, %v", object, err)
	}
	assertNoTransientLocalFiles(t, base)
	reserved := localMetadataPath("object", "object")
	if _, err = CleanKey(reserved, 0); !errors.Is(err, ErrUnsafeKey) {
		t.Fatalf("CleanKey(%q) error = %v; want ErrUnsafeKey", reserved, err)
	}
}

func TestLocalBucket_ReservedNamespacesCannotBeAddressedOrListed(t *testing.T) {
	t.Parallel()
	bucket, err := NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reserved := []string{
		localLockName,
		localTransactionName,
		localStagingDirectoryName,
		localMetadataPath("object", "object"),
		localBackupPath("object", "object", "object"),
	}
	for _, key := range reserved {
		if _, err = bucket.Put(context.Background(), key, strings.NewReader("x"), PutOptions{}); !errors.Is(err, ErrUnsafeKey) {
			t.Errorf("Put(%q) error = %v; want ErrUnsafeKey", key, err)
		}
		if reader, _, getErr := bucket.Get(context.Background(), key); !errors.Is(getErr, ErrUnsafeKey) {
			if reader != nil {
				if closeErr := reader.Close(); closeErr != nil {
					t.Errorf("close unexpected reader for %q: %v", key, closeErr)
				}
			}
			t.Errorf("Get(%q) error = %v; want ErrUnsafeKey", key, getErr)
		}
		if err = bucket.Delete(context.Background(), key); !errors.Is(err, ErrUnsafeKey) {
			t.Errorf("Delete(%q) error = %v; want ErrUnsafeKey", key, err)
		}
		if _, err = bucket.Exists(context.Background(), key); !errors.Is(err, ErrUnsafeKey) {
			t.Errorf("Exists(%q) error = %v; want ErrUnsafeKey", key, err)
		}
		if _, err = bucket.Stat(context.Background(), key); !errors.Is(err, ErrUnsafeKey) {
			t.Errorf("Stat(%q) error = %v; want ErrUnsafeKey", key, err)
		}
		if _, err = bucket.URL(context.Background(), key); !errors.Is(err, ErrUnsafeKey) {
			t.Errorf("URL(%q) error = %v; want ErrUnsafeKey", key, err)
		}
		if _, err = bucket.List(context.Background(), ListOptions{Prefix: key}); !errors.Is(err, ErrUnsafeKey) {
			t.Errorf("List(%q) error = %v; want ErrUnsafeKey", key, err)
		}
	}
}

func TestLocalBucket_KeySizeBoundary(t *testing.T) {
	t.Parallel()
	segment := strings.Repeat("k", 200)
	exact := strings.Join([]string{segment, segment, segment, segment, segment, strings.Repeat("k", 19)}, "/")
	if len(exact) != MaxKeyBytes {
		t.Fatalf("test key length = %d; want %d", len(exact), MaxKeyBytes)
	}
	if err := MustBeLocal(exact); err != nil {
		t.Fatalf("MustBeLocal exact key boundary: %v", err)
	}
	if _, err := cleanS3Key(exact); err != nil {
		t.Fatalf("cleanS3Key exact key boundary: %v", err)
	}
	bucket, err := NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bucket.Put(context.Background(), exact, strings.NewReader("x"), PutOptions{}); err != nil {
		t.Fatalf("Put exact key boundary: %v", err)
	}
	if _, err = bucket.Stat(context.Background(), exact); err != nil {
		t.Fatalf("Stat exact key boundary: %v", err)
	}
	if _, err = bucket.Put(context.Background(), exact+"x", strings.NewReader("x"), PutOptions{}); !errors.Is(err, ErrUnsafeKey) {
		t.Fatalf("Put key boundary + 1 error = %v; want ErrUnsafeKey", err)
	}
	if err = MustBeLocal(exact + "x"); !errors.Is(err, ErrUnsafeKey) {
		t.Fatalf("MustBeLocal key boundary + 1 error = %v; want ErrUnsafeKey", err)
	}
	if _, err = cleanS3Key(exact + "x"); !errors.Is(err, ErrUnsafeKey) {
		t.Fatalf("cleanS3Key boundary + 1 error = %v; want ErrUnsafeKey", err)
	}
}

func TestVerifyLocalObjectAttributes_RestoresOffsetOnCancellation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "object")
	if err := os.WriteFile(path, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close digest test file: %v", closeErr)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attrs := localAttributes{BodySize: info.Size(), BodySHA256: strings.Repeat("0", sha256.Size*2)}
	if err = verifyLocalObjectAttributes(ctx, file, info, attrs); !errors.Is(err, context.Canceled) {
		t.Fatalf("verify canceled error = %v; want context.Canceled", err)
	}
	position, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if position != 0 {
		t.Fatalf("file offset after canceled digest = %d; want 0", position)
	}
}

func TestNewBucketBoundsS3Initialization(t *testing.T) {
	t.Parallel()
	const timeout = 10 * time.Millisecond
	var deadline, called time.Time
	factory := func(ctx context.Context, _ S3Options) (*S3Bucket, error) {
		called = time.Now()
		d, ok := ctx.Deadline()
		if !ok {
			t.Fatal("S3 constructor context has no deadline")
		}
		deadline = d
		<-ctx.Done()
		return nil, ctx.Err()
	}
	// Bound the deadline by timestamps around the call, not by time left, so
	// a slow scheduler cannot fail a correct constructor.
	start := time.Now()
	_, err := newBucketWithS3Factory(
		Options{Backend: "s3"}, slog.New(slog.DiscardHandler), timeout, factory,
	)
	if deadline.Before(start.Add(timeout)) || deadline.After(called.Add(timeout)) {
		t.Fatalf("S3 constructor deadline = start+%v; want within [start+%v, call+%v]",
			deadline.Sub(start), timeout, timeout)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("newBucketWithS3Factory error = %v; want context deadline", err)
	}
}

type countingFS struct {
	fs.FS
	opens   int
	entries int
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.opens++
	file, err := c.FS.Open(name)
	if err != nil {
		return nil, err
	}
	dir, ok := file.(fs.ReadDirFile)
	if !ok {
		return file, nil
	}
	return &countingDirFile{File: file, dir: dir, owner: c}, nil
}

type countingDirFile struct {
	fs.File
	dir   fs.ReadDirFile
	owner *countingFS
}

func (f *countingDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := f.dir.ReadDir(n)
	f.owner.entries += len(entries)
	return entries, err
}

func TestWalkLocalObjects_PrunesAbsentPrefix(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for i := range 200 {
		dir := filepath.Join(root, fmt.Sprintf("unrelated-%03d", i))
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "object"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	counted := &countingFS{FS: os.DirFS(root)}
	objects, err := walkLocalObjects(context.Background(), counted, localListQuery{
		prefix: "absent/prefix/", limit: 1, budget: localListWorkBudget,
	})
	if err != nil {
		t.Fatalf("walkLocalObjects: %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("objects = %+v; want empty", objects)
	}
	if counted.opens > 2 || counted.entries != 0 {
		t.Fatalf("absent prefix opened %d paths and read %d entries; want direct miss", counted.opens, counted.entries)
	}
}

func TestWalkLocalObjects_CapsVisitedEntries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		if err := os.WriteFile(filepath.Join(target, fmt.Sprintf("other-%03d", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	counted := &countingFS{FS: os.DirFS(root)}
	_, err := walkLocalObjects(context.Background(), counted, localListQuery{
		prefix: "target/no-match", limit: 1, budget: 80,
	})
	if !errors.Is(err, ErrListWorkLimit) {
		t.Fatalf("walkLocalObjects error = %v; want ErrListWorkLimit", err)
	}
	if counted.entries > 80 {
		t.Fatalf("visited %d entries; want at most 80", counted.entries)
	}
}
