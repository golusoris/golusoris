// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package testimages owns immutable container references used by integration tests.
package testimages

import (
	"errors"
	"strings"

	"github.com/testcontainers/testcontainers-go"
)

const (
	// Postgres is the default PostgreSQL integration-test image.
	Postgres = "postgres:17-alpine@sha256:f02121de6f74d30d8a94cd1d9584125e2178d7e6c377d8130112d4e52d867995"
	// Timescale is the PostgreSQL 17 TimescaleDB integration-test image.
	Timescale = "timescale/timescaledb:2.30.0-pg17@sha256:3113d12b78392c064aa7475caf7a52b447b29ddd4f9bfd23526733fcb03e3459"
	// Redis is the default Redis integration-test image.
	Redis = "redis:7-alpine@sha256:520775a41a63e77e06c73e35d2fd9cc15921a609516818796b4ecbb813078bc7"
	// NATS is the default NATS integration-test image.
	NATS = "nats:2-alpine@sha256:ac8f88a6494bffc2c2a5289a0ca61cb28a9145c11ba5677cf24265d07f46d8d4"
	// ClickHouse is the default ClickHouse integration-test image.
	ClickHouse = "clickhouse/clickhouse-server:24@sha256:2113951827761e37c386b37f716dbdf8522b9172488a44b97a96fe6059e172a8"
	// Redpanda is the Kafka-compatible integration-test image.
	Redpanda = "redpandadata/redpanda:v24.3.1@sha256:f2f8bb89f1a0747cc6f86440cb3a0916e981e136e1d72392bab179f73492fb0f"
	// VersityGW is the S3-compatible gateway that enforces SigV4 for storage tests.
	VersityGW = "versity/versitygw:v1.8.0@sha256:30292fc2eeacc67a36993b01f7a7a5e3361a19cced0e80c1d71cfa2a4b0a2499"
	// FakeGCSServer is the GCS emulator for the storage/gcs module tests.
	FakeGCSServer = "fsouza/fake-gcs-server:1.56.1@sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef"
	// Azurite is the Azure Storage emulator for the storage/azblob module tests.
	Azurite = "mcr.microsoft.com/azure-storage/azurite:3.37.0@sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5"
	// Ryuk is the testcontainers resource-reaper image.
	Ryuk = "testcontainers/ryuk:0.14.0@sha256:7c1a8a9a47c780ed0f983770a662f80deb115d95cce3e2daa3d12115b8cd28f0"
	// ClamAV is the opt-in malware-scanner integration-test image.
	ClamAV = "clamav/clamav:1.5_base-debian@sha256:0481636f876a3d338ab2a9871888f8a75968f428a423e872ec98a73c6687e1ce"
)

var errMutableImage = errors.New("test image must use tag@sha256:<64 lowercase hex characters>")

// Validate rejects mutable or malformed test image references.
func Validate(image string) error {
	repository, digest, found := strings.Cut(image, "@sha256:")
	if !found || !isTaggedRepository(repository) || !isSHA256(digest) {
		return errMutableImage
	}
	return nil
}

func isTaggedRepository(repository string) bool {
	if repository == "" || strings.ContainsAny(repository, "@ \t\r\n") {
		return false
	}
	lastSlash := strings.LastIndexByte(repository, '/')
	lastColon := strings.LastIndexByte(repository, ':')
	return lastColon > lastSlash && lastColon < len(repository)-1
}

func isSHA256(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for i := range 64 {
		if c := digest[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// WithPinnedReaper forces testcontainers to use the immutable Ryuk reference.
func WithPinnedReaper() testcontainers.ContainerCustomizer {
	return testcontainers.CustomizeRequestOption(func(req *testcontainers.GenericContainerRequest) error {
		req.ReaperImage = Ryuk
		return nil
	})
}
