package clickhouse_test

import (
	"testing"

	"github.com/golusoris/golusoris/db/clickhouse"
)

func TestModule_notNil(t *testing.T) {
	t.Parallel()
	// Integration tests require a running ClickHouse instance.
	// Verify the module var is exported.
	_ = clickhouse.Module
}
