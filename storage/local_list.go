// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	pathpkg "path"
	"slices"
	"strings"

	gerr "github.com/golusoris/golusoris/core/errors"
)

// List implements [Bucket]. Keys come back in ascending byte-wise order, so
// every visited directory is read in full and sorted. One call reads at most
// 65536 directory entries; past that it returns the objects found so far, or
// [ErrListWorkLimit] when there are none.
func (b *LocalBucket) List(ctx context.Context, opts ListOptions) (out []Object, err error) {
	query, empty, err := NormalizeListOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("storage: list local objects: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: list local objects: %w", err)
	}
	if empty {
		return nil, nil
	}
	root, err := b.openRoot()
	if err != nil {
		return nil, err
	}
	defer gerr.CloseInto(root, &err, "storage: close base root")
	operationLock, err := b.lockAndRecover(ctx, root, "list before walk")
	if err != nil {
		return nil, err
	}
	defer gerr.CloseInto(operationLock, &err, "storage: release local list lock")
	out, err = walkLocalObjects(ctx, root.FS(), localListQuery{
		prefix: query.Prefix, after: query.StartAfter, limit: query.Limit, budget: localListWorkBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: list local objects: %w", err)
	}
	return out, nil
}

var errListLimitReached = errors.New("storage: list limit reached")

const (
	localListReadBatch = 32
	// localListWorkBudget caps the directory entries one List call reads,
	// which also caps the entries it holds for sorting.
	localListWorkBudget = 1 << 16
)

// localListQuery is a validated listing: clean prefix and start position,
// object limit, and directory-entry work budget.
type localListQuery struct {
	prefix string
	after  string
	limit  int
	budget int
}

// localListEntry is a directory entry that can contribute keys. key is the
// object key, or for a directory its key prefix ending in "/": sorting a
// directory's entries by key then yields byte-wise order across subtrees.
type localListEntry struct {
	key   string
	entry fs.DirEntry
}

func walkLocalObjects(ctx context.Context, rootFS fs.FS, query localListQuery) ([]Object, error) {
	walker := localListWalker{
		check:     ctx.Err,
		rootFS:    rootFS,
		query:     query,
		remaining: query.budget,
		out:       make([]Object, 0, min(query.limit, 16)),
	}
	err := walker.walk(localListStart(query.prefix))
	switch {
	case errors.Is(err, errListLimitReached):
		err = nil
	case errors.Is(err, ErrListWorkLimit) && len(walker.out) > 0:
		// Objects found so far precede every unread key: a valid short page.
		err = nil
	}
	return walker.out, err
}

type localListWalker struct {
	check     func() error
	rootFS    fs.FS
	query     localListQuery
	remaining int
	out       []Object
}

func (w *localListWalker) walk(start string) error {
	if err := w.check(); err != nil {
		return err
	}
	info, err := fs.Lstat(w.rootFS, start)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: inspect list root %q: %w", start, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil
	}
	if !info.IsDir() {
		return w.appendInfo(start, info)
	}
	return w.walkTree(start)
}

// walkTree visits start's subtree depth-first in key order with an explicit
// stack of sorted directory listings. Each step consumes one read entry or
// pops one listing, so 2*budget+2 steps always suffice.
func (w *localListWalker) walkTree(start string) error {
	first, err := w.readSorted(start)
	if err != nil {
		return err
	}
	stack := [][]localListEntry{first}
	for range 2*w.query.budget + 2 {
		if err = w.check(); err != nil {
			return err
		}
		top := len(stack) - 1
		if top < 0 {
			return nil
		}
		if len(stack[top]) == 0 {
			stack = stack[:top]
			continue
		}
		next := stack[top][0]
		stack[top] = stack[top][1:]
		if !next.entry.IsDir() {
			if err = w.appendEntry(next); err != nil {
				return err
			}
			continue
		}
		children, readErr := w.readSorted(strings.TrimSuffix(next.key, "/"))
		if readErr != nil {
			return readErr
		}
		stack = append(stack, children)
	}
	return ErrListWorkLimit
}

func (w *localListWalker) readSorted(name string) (entries []localListEntry, err error) {
	file, err := w.rootFS.Open(name)
	if err != nil {
		return nil, fmt.Errorf("storage: open listed directory %q: %w", name, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("storage: close listed directory %q: %w", name, closeErr))
		}
	}()
	directory, ok := file.(fs.ReadDirFile)
	if !ok {
		return nil, fmt.Errorf("storage: listed directory %q does not support bounded reads", name)
	}
	entries, err = w.readDirectory(name, directory)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b localListEntry) int { return strings.Compare(a.key, b.key) })
	return entries, nil
}

func (w *localListWalker) readDirectory(name string, directory fs.ReadDirFile) ([]localListEntry, error) {
	var kept []localListEntry
	for w.remaining > 0 {
		if err := w.check(); err != nil {
			return nil, err
		}
		entries, readErr := directory.ReadDir(min(localListReadBatch, w.remaining))
		w.remaining -= len(entries)
		for _, entry := range entries {
			if key, ok := w.keep(name, entry); ok {
				kept = append(kept, localListEntry{key: key, entry: entry})
			}
		}
		if errors.Is(readErr, io.EOF) {
			return kept, nil
		}
		if readErr != nil {
			return nil, fmt.Errorf("storage: read listed directory %q: %w", name, readErr)
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("storage: read listed directory %q: %w", name, io.ErrNoProgress)
		}
	}
	return nil, ErrListWorkLimit
}

// keep returns entry's sort key and whether it can hold a key under the
// prefix that sorts after the start position.
func (w *localListWalker) keep(directory string, entry fs.DirEntry) (string, bool) {
	if entry.Type()&fs.ModeSymlink != 0 || isLocalInternalName(entry.Name()) {
		return "", false
	}
	name := pathpkg.Join(directory, entry.Name())
	if entry.IsDir() {
		key := name + "/"
		return key, localDirectoryCanMatch(name, w.query.prefix) && localSubtreeAfter(key, w.query.after)
	}
	return name, strings.HasPrefix(name, w.query.prefix) && name > w.query.after
}

func (w *localListWalker) appendEntry(next localListEntry) error {
	info, err := next.entry.Info()
	if err != nil {
		return fmt.Errorf("storage: stat listed object %q: %w", next.key, err)
	}
	return w.appendInfo(next.key, info)
}

func (w *localListWalker) appendInfo(name string, info fs.FileInfo) error {
	if isLocalInternalName(pathpkg.Base(name)) || !strings.HasPrefix(name, w.query.prefix) || name <= w.query.after {
		return nil
	}
	w.out = append(w.out, Object{Key: name, Size: info.Size(), LastModified: info.ModTime()})
	if len(w.out) >= w.query.limit {
		return errListLimitReached
	}
	return nil
}

func isLocalInternalName(name string) bool {
	return isLocalTempName(name) || isLocalMetadataName(name) || isLocalControlName(name)
}

func localListStart(prefix string) string {
	if before, ok := strings.CutSuffix(prefix, "/"); ok {
		return before
	}
	if separator := strings.LastIndexByte(prefix, '/'); separator >= 0 {
		return prefix[:separator]
	}
	return "."
}

func localDirectoryCanMatch(directory, prefix string) bool {
	if directory == "." || prefix == "" {
		return true
	}
	directoryPrefix := directory + "/"
	return strings.HasPrefix(prefix, directoryPrefix) || strings.HasPrefix(directoryPrefix, prefix)
}

// localSubtreeAfter reports whether a directory, whose keys all begin with
// dirKey (ending in "/"), can hold a key sorting after after: either every
// key in it does, or after itself lies inside it.
func localSubtreeAfter(dirKey, after string) bool {
	return dirKey > after || strings.HasPrefix(after, dirKey)
}
