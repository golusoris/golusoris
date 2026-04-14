package linking_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/linking"
)

func TestService_LinkLookup(t *testing.T) {
	t.Parallel()

	svc := linking.New(linking.NewMemoryStore())

	require.NoError(t, svc.Link(context.Background(), "u-1", "google", "g-123", "u@x"))

	uid, err := svc.Lookup(context.Background(), "google", "g-123")
	require.NoError(t, err)
	require.Equal(t, "u-1", uid)
}

func TestService_LinkConflict(t *testing.T) {
	t.Parallel()

	svc := linking.New(linking.NewMemoryStore())
	ctx := context.Background()

	require.NoError(t, svc.Link(ctx, "u-1", "github", "gh-1", ""))
	err := svc.Link(ctx, "u-2", "github", "gh-1", "")
	require.Error(t, err)
}

func TestService_ListAndUnlink(t *testing.T) {
	t.Parallel()

	svc := linking.New(linking.NewMemoryStore())
	ctx := context.Background()
	require.NoError(t, svc.Link(ctx, "u-3", "google", "g-x", "x@y"))
	require.NoError(t, svc.Link(ctx, "u-3", "github", "gh-x", "x@y"))

	list, err := svc.List(ctx, "u-3")
	require.NoError(t, err)
	require.Len(t, list, 2)

	require.NoError(t, svc.Unlink(ctx, "google", "g-x"))

	list, err = svc.List(ctx, "u-3")
	require.NoError(t, err)
	require.Len(t, list, 1)
}
