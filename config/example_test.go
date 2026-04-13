package config_test

import (
	"fmt"
	"os"

	"github.com/golusoris/golusoris/config"
)

// ExampleNew demonstrates loading config from environment variables. APP_DB_HOST
// becomes "db.host" with the default delimiter.
func ExampleNew() {
	_ = os.Setenv("APP_DB_HOST", "primary.local")
	defer os.Unsetenv("APP_DB_HOST")

	c, err := config.New(config.Options{EnvPrefix: "APP_"})
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(c.String("db.host"))
	// Output: primary.local
}

// ExampleConfig_Unmarshal shows decoding into a struct.
func ExampleConfig_Unmarshal() {
	_ = os.Setenv("APP_DB_HOST", "primary.local")
	_ = os.Setenv("APP_DB_PORT", "5432")
	defer func() {
		_ = os.Unsetenv("APP_DB_HOST")
		_ = os.Unsetenv("APP_DB_PORT")
	}()

	c, _ := config.New(config.Options{EnvPrefix: "APP_"})

	var cfg struct {
		Host string `koanf:"host"`
		Port int    `koanf:"port"`
	}
	if err := c.Unmarshal("db", &cfg); err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Printf("%s:%d\n", cfg.Host, cfg.Port)
	// Output: primary.local:5432
}
