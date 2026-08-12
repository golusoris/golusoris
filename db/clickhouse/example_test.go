package clickhouse_test

import (
	"fmt"

	"github.com/golusoris/golusoris/db/clickhouse"
)

// ExampleConfig shows the config fields and their expected types.
func ExampleConfig() {
	cfg := clickhouse.Config{
		Addr:     []string{"ch1:9000", "ch2:9000"},
		Database: "analytics",
		Username: "reader",
		TLS:      true,
	}
	fmt.Println(len(cfg.Addr), cfg.Database, cfg.Username, cfg.TLS)
	// Output: 2 analytics reader true
}
