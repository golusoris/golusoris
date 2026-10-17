// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package tus

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSaveCompletion_waitsForOpenReader(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	id := "test-save-open-reader"
	record := newCompletionRecord(CompletedUpload{ID: id, Key: "keys/" + id})
	require.NoError(t, scratch.SaveCompletion(ctx, record))

	completionPath, err := scratch.completionPath(id)
	require.NoError(t, err)

	reader, err := os.Open(completionPath)
	require.NoError(t, err)
	var closeOnce sync.Once
	closeReader := func() { closeOnce.Do(func() { _ = reader.Close() }) }
	t.Cleanup(closeReader)

	errCh := make(chan error, 1)
	record.Upload.Key = "keys/" + id + "-updated"
	go func() {
		errCh <- scratch.SaveCompletion(ctx, record)
	}()

	time.Sleep(50 * time.Millisecond)
	closeReader()

	select {
	case saveErr := <-errCh:
		require.NoError(t, saveErr)
	case <-time.After(5 * time.Second):
		t.Fatal("SaveCompletion timed out waiting for reader close")
	}
}

func TestDeleteCompletion_waitsForOpenReader(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	id := "test-delete-open-reader"
	record := newCompletionRecord(CompletedUpload{ID: id, Key: "keys/" + id})
	require.NoError(t, scratch.SaveCompletion(ctx, record))

	completionPath, err := scratch.completionPath(id)
	require.NoError(t, err)

	reader, err := os.Open(completionPath)
	require.NoError(t, err)
	var closeOnce sync.Once
	closeReader := func() { closeOnce.Do(func() { _ = reader.Close() }) }
	t.Cleanup(closeReader)

	errCh := make(chan error, 1)
	go func() {
		errCh <- scratch.DeleteCompletion(ctx, id)
	}()

	time.Sleep(50 * time.Millisecond)
	closeReader()

	select {
	case delErr := <-errCh:
		require.NoError(t, delErr)
	case <-time.After(5 * time.Second):
		t.Fatal("DeleteCompletion timed out waiting for reader close")
	}

	_, err = os.Stat(completionPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}
