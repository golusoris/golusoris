package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/storage"
)

func TestLocalBucket(t *testing.T) {
	dir := t.TempDir()
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Put
	obj, err := b.Put(ctx, "dir/file.txt", strings.NewReader("hello"), storage.PutOptions{})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Key != "dir/file.txt" {
		t.Fatalf("unexpected key: %q", obj.Key)
	}

	// Exists
	ok, err := b.Exists(ctx, "dir/file.txt")
	if err != nil || !ok {
		t.Fatalf("Exists: ok=%v err=%v", ok, err)
	}

	// Get
	rc, got, err := b.Get(ctx, "dir/file.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close() //nolint:errcheck
	if got.Size != 5 {
		t.Fatalf("size: expected 5, got %d", got.Size)
	}

	// List
	objects, err := b.List(ctx, storage.ListOptions{Prefix: "dir/"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}

	// Delete
	if err := b.Delete(ctx, "dir/file.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ok, _ = b.Exists(ctx, "dir/file.txt")
	if ok {
		t.Fatal("expected file to be deleted")
	}

	// Delete non-existent — should not error
	if err := b.Delete(ctx, "does-not-exist.txt"); err != nil {
		t.Fatalf("Delete non-existent: %v", err)
	}
}

func TestLocalBucket_pathTraversal(t *testing.T) {
	dir := t.TempDir()
	b, _ := storage.NewLocalBucket(dir)

	_, err := b.Put(context.Background(), "../evil.txt", strings.NewReader("x"), storage.PutOptions{})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestLocalBucket_notFound(t *testing.T) {
	dir := t.TempDir()
	b, _ := storage.NewLocalBucket(dir)

	_, _, err := b.Get(context.Background(), "missing.txt")
	if err == nil {
		t.Fatal("expected error")
	}
}
