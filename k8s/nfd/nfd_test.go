// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nfd_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/k8s/nfd"
)

func TestWriteFeatureFile_writesSortedLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := nfd.WriteFeatureFile(dir, "vmafx", map[string]string{
		"vmafx.io/backend":                   "cuda",
		"feature.node.kubernetes.io/gpu.idx": "0",
		"gpu.feature.node.kubernetes.io/mem": "",
	})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "vmafx"))
	require.NoError(t, err)
	require.Equal(t, "feature.node.kubernetes.io/gpu.idx=0\ngpu.feature.node.kubernetes.io/mem=\nvmafx.io/backend=cuda\n", string(got))
	assertNoTemps(t, dir)
}

func TestWriteFeatureFile_modeIsWorldReadable(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	dir := t.TempDir()
	require.NoError(t, nfd.WriteFeatureFile(dir, "f", map[string]string{"example.com/a": "b"}))
	fi, err := os.Stat(filepath.Join(dir, "f"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
}

func TestWriteFeatureFileUntil_expiryHeaderFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	expiry := time.Date(2030, 7, 29, 11, 22, 33, 0, time.FixedZone("CEST", 2*3600))
	require.NoError(t, nfd.WriteFeatureFileUntil(dir, "f", map[string]string{"example.com/a": "b"}, expiry))

	got, err := os.ReadFile(filepath.Join(dir, "f"))
	require.NoError(t, err)
	require.Equal(t, "# +expiry-time=2030-07-29T09:22:33Z\nexample.com/a=b\n", string(got))
}

func TestWriteFeatureFile_emptyLabelsWritesEmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, nfd.WriteFeatureFile(dir, "f", nil))
	got, err := os.ReadFile(filepath.Join(dir, "f"))
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestWriteFeatureFile_rejectsInvalidNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", ".hidden", "a/b", `a\b`, "..", strings.Repeat("n", 201)} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			err := nfd.WriteFeatureFile(dir, name, map[string]string{"example.com/a": "b"})
			require.ErrorIs(t, err, nfd.ErrInvalidName)
			assertEmptyDir(t, dir)
		})
	}
}

func TestWriteFeatureFile_acceptsMaxLengthName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := strings.Repeat("n", 200)
	require.NoError(t, nfd.WriteFeatureFile(dir, name, map[string]string{"example.com/a": "b"}))
	_, err := os.Stat(filepath.Join(dir, name))
	require.NoError(t, err)
}

func TestValidateLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		key, value string
		ok         bool
	}{
		{"example.com/a", "b", true},
		{"example.com/a", "", true},
		{"example.com/a", strings.Repeat("v", 63), true},
		{"feature.node.kubernetes.io/x", "1", true},
		{"profile.node.kubernetes.io/x", "1", true},
		{"sub.feature.node.kubernetes.io/x", "1", true},
		{"example.com/" + strings.Repeat("k", 63), "v", true},
		{"unprefixed", "v", false},
		{"kubernetes.io/arch", "amd64", false},
		{"node.kubernetes.io/x", "1", false},
		{"example.com/a", strings.Repeat("v", 64), false},
		{"example.com/a", "has space", false},
		{"example.com/a", "line\nbreak", false},
		{"example.com/a=b", "v", false},
		{"Example_com/a", "v", false},
		{"example.com/" + strings.Repeat("k", 64), "v", false},
		{"example.com/-a", "v", false},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Parallel()
			err := nfd.ValidateLabel(tc.key, tc.value)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, nfd.ErrInvalidLabel)
		})
	}
}

func TestWriteFeatureFile_invalidLabelLeavesExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, nfd.WriteFeatureFile(dir, "f", map[string]string{"example.com/a": "old"}))

	err := nfd.WriteFeatureFile(dir, "f", map[string]string{"example.com/a": "new", "kubernetes.io/x": "y"})
	require.ErrorIs(t, err, nfd.ErrInvalidLabel)

	got, err := os.ReadFile(filepath.Join(dir, "f"))
	require.NoError(t, err)
	require.Equal(t, "example.com/a=old\n", string(got))
	assertNoTemps(t, dir)
}

func TestWriteFeatureFile_sizeLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, nfd.WriteFeatureFile(dir, "exact", sizedLabels(t, nfd.MaxFileSize)))
	fi, err := os.Stat(filepath.Join(dir, "exact"))
	require.NoError(t, err)
	require.Equal(t, int64(nfd.MaxFileSize), fi.Size())

	err = nfd.WriteFeatureFile(dir, "over", sizedLabels(t, nfd.MaxFileSize+1))
	require.ErrorIs(t, err, nfd.ErrTooLarge)
	_, statErr := os.Stat(filepath.Join(dir, "over"))
	require.True(t, errors.Is(statErr, os.ErrNotExist))
}

// sizedLabels returns labels whose rendered file is exactly total bytes:
// 70-byte lines ("example.com/kNNNN=" + 51 bytes + newline), with the
// remainder spread one byte per line.
func sizedLabels(t *testing.T, total int) map[string]string {
	t.Helper()
	n, rem := total/70, total%70
	require.LessOrEqual(t, rem, n)
	labels := make(map[string]string, n)
	for i := range n {
		width := 51
		if i < rem {
			width = 52
		}
		labels["example.com/k"+leftPad(i)] = strings.Repeat("v", width)
	}
	return labels
}

func TestWriteFeatureFile_missingDirFails(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "absent")
	err := nfd.WriteFeatureFile(dir, "f", map[string]string{"example.com/a": "b"})
	require.ErrorContains(t, err, "nfd: create temp file")
}

// TestWriteFeatureFile_readersNeverSeePartialFile rewrites the file while a
// reader polls it: every observed content must be one complete version.
func TestWriteFeatureFile_readersNeverSeePartialFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	big := func(v string) map[string]string {
		m := make(map[string]string, 400)
		for i := range 400 {
			m["example.com/k"+leftPad(i)] = v
		}
		return m
	}
	require.NoError(t, nfd.WriteFeatureFile(dir, "f", big("a")))
	want := map[string]bool{}
	for _, v := range []string{"a", "b"} {
		tmp := t.TempDir()
		require.NoError(t, nfd.WriteFeatureFile(tmp, "f", big(v)))
		b, err := os.ReadFile(filepath.Join(tmp, "f"))
		require.NoError(t, err)
		want[string(b)] = true
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for i := range 200 {
			v := "a"
			if i%2 == 1 {
				v = "b"
			}
			if err := nfd.WriteFeatureFile(dir, "f", big(v)); err != nil {
				t.Error(err)
				break
			}
		}
		close(stop)
	})
	reads := 0
	for done := false; !done && reads < 1_000_000; reads++ {
		select {
		case <-stop:
			done = true
		default:
		}
		b, err := os.ReadFile(filepath.Join(dir, "f"))
		if runtime.GOOS == "windows" {
			// Windows cannot replace a file while a reader holds it open, so a
			// reader that never pauses would starve every rename; poll instead.
			time.Sleep(time.Millisecond)
			if err != nil {
				continue // a rename can briefly deny sharing on Windows
			}
		}
		require.NoError(t, err)
		require.True(t, want[string(b)], "observed a partial file of %d bytes", len(b))
	}
	wg.Wait()
	assertNoTemps(t, dir)
}

func leftPad(i int) string {
	s := strconv.Itoa(i)
	return strings.Repeat("0", 4-len(s)) + s
}

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), "."), "temp file %s left behind", e.Name())
	}
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}
