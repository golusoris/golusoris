// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	tusd "github.com/tus/tusd/v2/pkg/handler"
)

func TestScratchInfoReadBoundary(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "exact limit", size: maxScratchInfoBytes},
		{name: "one byte over", size: maxScratchInfoBytes + 1, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scratch := newScratchSecurityFixture(t)
			const id = "bounded-info"
			writeScratchInfoFixture(t, scratch, id, tt.size)

			entry, err := scratch.Get(context.Background(), id)

			if tt.wantErr {
				require.ErrorContains(t, err, "size limit")
				require.Nil(t, entry)
				return
			}
			require.NoError(t, err)
			info, err := entry.GetInfo(context.Background())
			require.NoError(t, err)
			require.Equal(t, id, info.ID)
		})
	}
}

func TestScratchCompletionReadBoundary(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "exact limit", size: maxCompletionRecordBytes},
		{name: "one byte over", size: maxCompletionRecordBytes + 1, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scratch := newScratchSecurityFixture(t)
			const id = "bounded-completion"
			writeCompletionFixture(t, scratch, id, tt.size)

			record, err := scratch.Completion(context.Background(), id)

			if tt.wantErr {
				require.ErrorContains(t, err, "size limit")
				require.Equal(t, completionRecord{}, record)
				return
			}
			require.NoError(t, err)
			require.Equal(t, id, record.Upload.ID)
		})
	}
}

func TestScratchStateRejectsSymlinksAndNonRegularFiles(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"info", "completion"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			for _, kind := range []string{"symlink", "directory"} {
				t.Run(kind, func(t *testing.T) {
					t.Parallel()
					scratch := newScratchSecurityFixture(t)
					const id = "nonregular"
					path := scratchStatePath(t, scratch, id, state)
					if state == "info" {
						binPath, err := scratch.binPath(id)
						require.NoError(t, err)
						require.NoError(t, os.WriteFile(binPath, nil, scratchFilePerm))
					}
					switch kind {
					case "symlink":
						target := filepath.Join(t.TempDir(), "state.json")
						require.NoError(t, os.WriteFile(target, validScratchState(t, id, state), scratchFilePerm))
						if err := os.Symlink(target, path); err != nil {
							t.Skipf("symlink unsupported: %v", err)
						}
					case "directory":
						require.NoError(t, os.Mkdir(path, scratchDirPerm))
					}

					err := readScratchState(t, scratch, id, state)

					require.ErrorContains(t, err, "regular file")
				})
			}
		})
	}
}

func TestDeclareLengthRejectsSymlinkedInfoFile(t *testing.T) {
	t.Parallel()
	scratch := newScratchSecurityFixture(t)
	entry, err := scratch.Create(context.Background(), tusd.FileInfo{
		ID: "symlinked-info", SizeIsDeferred: true,
	})
	require.NoError(t, err)
	infoPath, err := scratch.infoPath("symlinked-info")
	require.NoError(t, err)
	require.NoError(t, os.Remove(infoPath))
	victim := filepath.Join(t.TempDir(), "victim")
	require.NoError(t, os.WriteFile(victim, []byte("do-not-overwrite"), scratchFilePerm))
	if err = os.Symlink(victim, infoPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	err = entry.DeclareLength(context.Background(), 42)

	require.ErrorContains(t, err, "regular file")
	data, readErr := os.ReadFile(victim)
	require.NoError(t, readErr)
	require.Equal(t, "do-not-overwrite", string(data))
	info, infoErr := entry.GetInfo(context.Background())
	require.NoError(t, infoErr)
	require.True(t, info.SizeIsDeferred)
}

func TestScratchInfoRejectsMismatchedUploadID(t *testing.T) {
	t.Parallel()
	scratch := newScratchSecurityFixture(t)
	const requestedID = "requested"
	binPath, err := scratch.binPath(requestedID)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(binPath, nil, scratchFilePerm))
	infoPath, err := scratch.infoPath(requestedID)
	require.NoError(t, err)
	data, err := json.Marshal(tusd.FileInfo{ID: "redirected"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(infoPath, data, scratchFilePerm))

	entry, err := scratch.Get(context.Background(), requestedID)

	require.Nil(t, entry)
	require.ErrorContains(t, err, "id mismatch")
}

func TestScratchStateReadHonorsPreCanceledContext(t *testing.T) {
	t.Parallel()
	scratch := newScratchSecurityFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, getErr := scratch.Get(ctx, "canceled")
	_, completionErr := scratch.Completion(ctx, "canceled")

	require.ErrorIs(t, getErr, context.Canceled)
	require.ErrorIs(t, completionErr, context.Canceled)
}

func newScratchSecurityFixture(t *testing.T) *localScratch {
	t.Helper()
	scratch, err := newLocalScratch(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scratch.Close()) })
	return scratch
}

func writeScratchInfoFixture(t *testing.T, scratch *localScratch, id string, size int) {
	t.Helper()
	binPath, err := scratch.binPath(id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(binPath, nil, scratchFilePerm))
	infoPath, err := scratch.infoPath(id)
	require.NoError(t, err)
	info := tusd.FileInfo{ID: id}
	require.NoError(t, os.WriteFile(infoPath, paddedJSON(t, info, size), scratchFilePerm))
}

func writeCompletionFixture(t *testing.T, scratch *localScratch, id string, size int) {
	t.Helper()
	path, err := scratch.completionPath(id)
	require.NoError(t, err)
	record := newCompletionRecord(CompletedUpload{ID: id, Key: "uploads/" + id})
	require.NoError(t, os.WriteFile(path, paddedJSON(t, record, size), scratchFilePerm))
}

func paddedJSON(t *testing.T, value any, size int) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	require.LessOrEqual(t, len(data), size)
	return append(data, bytes.Repeat([]byte{' '}, size-len(data))...)
}

func scratchStatePath(t *testing.T, scratch *localScratch, id, state string) string {
	t.Helper()
	var (
		path string
		err  error
	)
	if state == "info" {
		path, err = scratch.infoPath(id)
	} else {
		path, err = scratch.completionPath(id)
	}
	require.NoError(t, err)
	return path
}

func validScratchState(t *testing.T, id, state string) []byte {
	t.Helper()
	if state == "info" {
		data, err := json.Marshal(tusd.FileInfo{ID: id})
		require.NoError(t, err)
		return data
	}
	record := newCompletionRecord(CompletedUpload{ID: id, Key: "uploads/" + id})
	data, err := json.Marshal(record)
	require.NoError(t, err)
	return data
}

func readScratchState(t *testing.T, scratch *localScratch, id, state string) error {
	t.Helper()
	if state == "info" {
		_, err := scratch.Get(context.Background(), id)
		return err
	}
	_, err := scratch.Completion(context.Background(), id)
	return err
}
