package grpc

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_zeroFillsListen(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Listen != defaultListen {
		t.Errorf("Listen = %q, want %q", c.Listen, defaultListen)
	}
}

func TestWithDefaults_zeroFillsMaxSizes(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.MaxRecvSize != defaultMaxMsgBytes {
		t.Errorf("MaxRecvSize = %d, want %d", c.MaxRecvSize, defaultMaxMsgBytes)
	}
	if c.MaxSendSize != defaultMaxMsgBytes {
		t.Errorf("MaxSendSize = %d, want %d", c.MaxSendSize, defaultMaxMsgBytes)
	}
}

func TestWithDefaults_preservesExisting(t *testing.T) {
	t.Parallel()
	c := Config{Listen: ":1234", MaxRecvSize: 1024, MaxSendSize: 2048}.withDefaults()
	if c.Listen != ":1234" {
		t.Errorf("Listen = %q, want \":1234\"", c.Listen)
	}
	if c.MaxRecvSize != 1024 {
		t.Errorf("MaxRecvSize = %d, want 1024", c.MaxRecvSize)
	}
	if c.MaxSendSize != 2048 {
		t.Errorf("MaxSendSize = %d, want 2048", c.MaxSendSize)
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_GRPC_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != defaultListen {
		t.Errorf("Listen = %q, want %q", c.Listen, defaultListen)
	}
	if c.MaxRecvSize != defaultMaxMsgBytes {
		t.Errorf("MaxRecvSize = %d, want %d", c.MaxRecvSize, defaultMaxMsgBytes)
	}
	if c.MaxSendSize != defaultMaxMsgBytes {
		t.Errorf("MaxSendSize = %d, want %d", c.MaxSendSize, defaultMaxMsgBytes)
	}
}

func TestNewConnFactory_nonNil(t *testing.T) {
	t.Parallel()
	cf := newConnFactory()
	if cf == nil {
		t.Error("newConnFactory() returned nil")
	}
}
