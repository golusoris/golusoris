package snapshot_test

import (
	"testing"

	"github.com/golusoris/golusoris/testutil/snapshot"
)

func TestMatch_string(t *testing.T) {
	t.Parallel()
	snapshot.Match(t, "hello world")
}

func TestMatchJSON_struct(t *testing.T) {
	t.Parallel()
	type data struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	snapshot.MatchJSON(t, data{Name: "Alice", Age: 30})
}
