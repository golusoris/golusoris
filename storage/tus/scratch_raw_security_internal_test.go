// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	tusd "github.com/tus/tusd/v2/pkg/handler"
)

type rawScratchFixture struct {
	store    *bucketStore
	scratch  *localScratch
	upload   tusd.Upload
	id       string
	binPath  string
	infoPath string
	target   string
}

func TestScratchRawFileRejectsSymlinksAndNonRegularNodes(t *testing.T) {
	t.Parallel()
	for _, node := range []string{"external symlink", "internal symlink", "directory"} {
		t.Run(node, func(t *testing.T) {
			t.Parallel()
			for _, operation := range rawScratchOperations() {
				t.Run(operation.name, func(t *testing.T) {
					t.Parallel()
					fixture := newRawScratchFixture(t)
					fixture.replaceRawNode(t, node)

					err := operation.run(context.Background(), fixture)

					_, infoErr := os.Stat(fixture.infoPath)
					require.NoError(t, infoErr, "rejected operation must preserve recovery info")
					_, rawErr := os.Lstat(fixture.binPath)
					require.NoError(t, rawErr, "rejected operation must preserve suspect raw node")
					if fixture.target != "" {
						content, readErr := os.ReadFile(fixture.target)
						require.NoError(t, readErr)
						require.Equal(t, "sentinel", string(content))
					}
					require.ErrorIs(t, err, errScratchFileNotRegular)
				})
			}
		})
	}
}

func TestScratchRawFileAcceptsRegularNodes(t *testing.T) {
	t.Parallel()
	for _, operation := range rawScratchOperations() {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			fixture := newRawScratchFixture(t)

			err := operation.run(context.Background(), fixture)

			require.NoError(t, err)
		})
	}
}

func TestScratchFileIdentityValidationRejectsReplacement(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	beforePath := filepath.Join(root, "before")
	openedPath := filepath.Join(root, "opened")
	require.NoError(t, os.WriteFile(beforePath, []byte("before"), scratchFilePerm))
	require.NoError(t, os.WriteFile(openedPath, []byte("opened"), scratchFilePerm))
	before, err := os.Lstat(beforePath)
	require.NoError(t, err)
	opened, err := os.Stat(openedPath)
	require.NoError(t, err)

	err = validateScratchFileOpened("scratch data", before, opened)

	require.ErrorIs(t, err, errScratchFileChanged)
}

type rawScratchOperation struct {
	name string
	run  func(context.Context, *rawScratchFixture) error
}

func rawScratchOperations() []rawScratchOperation {
	return []rawScratchOperation{
		{
			name: "get",
			run: func(ctx context.Context, fixture *rawScratchFixture) error {
				_, err := fixture.store.GetUpload(ctx, fixture.id)
				return err
			},
		},
		{
			name: "write chunk",
			run: func(ctx context.Context, fixture *rawScratchFixture) error {
				_, err := fixture.upload.WriteChunk(ctx, int64(len("sentinel")), strings.NewReader("attack"))
				return err
			},
		},
		{
			name: "complete",
			run: func(ctx context.Context, fixture *rawScratchFixture) error {
				return fixture.upload.FinishUpload(ctx)
			},
		},
		{
			name: "abort",
			run: func(ctx context.Context, fixture *rawScratchFixture) error {
				return fixture.store.AsTerminatableUpload(fixture.upload).Terminate(ctx)
			},
		},
	}
}

func newRawScratchFixture(t *testing.T) *rawScratchFixture {
	t.Helper()
	store, _, scratch := newTestStore(t)
	t.Cleanup(func() { require.NoError(t, scratch.Close()) })
	upload, err := store.NewUpload(context.Background(), tusd.FileInfo{Size: 0})
	require.NoError(t, err)
	_, err = upload.WriteChunk(context.Background(), 0, strings.NewReader("sentinel"))
	require.NoError(t, err)
	info, err := upload.GetInfo(context.Background())
	require.NoError(t, err)
	binPath, err := scratch.binPath(info.ID)
	require.NoError(t, err)
	infoPath, err := scratch.infoPath(info.ID)
	require.NoError(t, err)
	return &rawScratchFixture{
		store: store, scratch: scratch, upload: upload, id: info.ID,
		binPath: binPath, infoPath: infoPath,
	}
}

func (f *rawScratchFixture) replaceRawNode(t *testing.T, node string) {
	t.Helper()
	require.NoError(t, os.Remove(f.binPath))
	switch node {
	case "external symlink":
		f.target = filepath.Join(t.TempDir(), "external-upload")
		require.NoError(t, os.WriteFile(f.target, []byte("sentinel"), scratchFilePerm))
		if err := os.Symlink(f.target, f.binPath); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
	case "internal symlink":
		const targetName = "internal-upload-target"
		f.target = filepath.Join(f.scratch.root, targetName)
		require.NoError(t, os.WriteFile(f.target, []byte("sentinel"), scratchFilePerm))
		if err := os.Symlink(targetName, f.binPath); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
	case "directory":
		require.NoError(t, os.Mkdir(f.binPath, scratchDirPerm))
	default:
		t.Fatalf("unknown raw-node fixture %q", node)
	}
}
