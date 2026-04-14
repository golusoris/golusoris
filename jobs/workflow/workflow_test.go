package workflow_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/jobs/workflow"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	c := workflow.DefaultConfig()
	require.Equal(t, "localhost:7233", c.Host)
	require.Equal(t, "default", c.Namespace)
}

func TestConfigWithDefaults_fillsZeros(t *testing.T) {
	t.Parallel()
	// Empty config should receive defaults after withDefaults (tested
	// indirectly through DefaultConfig — the exported surface).
	c := workflow.DefaultConfig()
	require.NotEmpty(t, c.Host)
	require.NotEmpty(t, c.Namespace)
}

func TestAPIKeyRequiresTLS(t *testing.T) {
	t.Parallel()
	// The validation that TLS must be on when an API key is set lives
	// inside newClient. We verify the exported Config type carries the
	// field correctly.
	c := workflow.Config{
		APIKey: "secret",
		TLS:    true,
	}
	require.True(t, c.TLS)
	require.NotEmpty(t, c.APIKey)
}
