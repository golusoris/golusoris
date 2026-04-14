package graphql_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	ourgql "github.com/golusoris/golusoris/graphql"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	d := ourgql.DefaultConfig()
	require.Equal(t, "/graphql", d.Path)
	require.Equal(t, "/graphql/playground", d.PlaygroundPath)
	require.True(t, d.Playground)
	require.Equal(t, 1000, d.ComplexityLimit)
	require.Equal(t, 100, d.APQCache)
}
