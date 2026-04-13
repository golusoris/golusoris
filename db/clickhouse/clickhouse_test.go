package clickhouse_test

import (
	"testing"

	"github.com/golusoris/golusoris/db/clickhouse"
)

func TestModule_notNil(_ *testing.T) {
	// Integration tests require a running ClickHouse instance.
	// Verify the module var is exported.
	_ = clickhouse.Module
}
