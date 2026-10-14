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
	Postgres = "postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873"
	// PostGIS is the PostgreSQL 18 PostGIS integration-test image.
	PostGIS = "postgis/postgis:18-3.6-alpine@sha256:ffcf0c4b904e41b9779f8098007fb5a9484025319c18c70cf8e1bcebb742b9b7"
	// Pgvector is the PostgreSQL 18 image with the pgvector extension for ai/vector tests.
	Pgvector = "pgvector/pgvector:0.8.7-pg18-trixie@sha256:9d9c930220cb9bf2f956d10a8f909cf9973d9672ca0278aef2c1e6facccad2e0"
	// Timescale is the PostgreSQL 17 TimescaleDB integration-test image.
	Timescale = "timescale/timescaledb:2.30.2-pg17@sha256:b346edcdb51a1fd6020e3965e0bd1c9f3406fa6d5fbce1e28f4852587ef934e2"
	// TimescaleApache is the Apache-2.0-only (OSS) build of the same TimescaleDB release.
	TimescaleApache = "timescale/timescaledb:2.30.2-pg17-oss@sha256:097e225077ef92e5c0e17e167d7ac71cb4e98762d31faa4ff0008c7955935889"
	// Redis is the default Redis integration-test image.
	Redis = "redis:8-alpine@sha256:3811787313eba226a2ef38658c6ccb91cd5e110edc89c37767de373120a0e5a0"
	// NATS is the default NATS integration-test image.
	NATS = "nats:2-alpine@sha256:2ae57dfba3063d5e25fecb75256118c0882a1a2d2cf7d0fba17a7c701df956f6"
	// ClickHouse is the default ClickHouse integration-test image.
	ClickHouse = "clickhouse/clickhouse-server:24@sha256:2113951827761e37c386b37f716dbdf8522b9172488a44b97a96fe6059e172a8"
	// Redpanda is the Kafka-compatible integration-test image.
	Redpanda = "redpandadata/redpanda:v26.2.4@sha256:c98c2f04a751e6646012cc701bac5183252cee44c88ba1dfa412e4c7124f2e89"
	// VersityGW is the S3-compatible gateway that enforces SigV4 for storage tests.
	VersityGW = "versity/versitygw:v1.9.0@sha256:eb7f786fcd91e72194e48fcf1ac16302d686bf8e3a71434bdbc067a6efd3fdc7"
	// FakeGCSServer is the GCS emulator for the storage/gcs module tests.
	FakeGCSServer = "fsouza/fake-gcs-server:1.56.1@sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef"
	// Azurite is the Azure Storage emulator for the storage/azblob module tests.
	Azurite = "mcr.microsoft.com/azure-storage/azurite:3.37.0@sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5"
	// Ryuk is the testcontainers resource-reaper image.
	Ryuk = "testcontainers/ryuk:0.14.0@sha256:7c1a8a9a47c780ed0f983770a662f80deb115d95cce3e2daa3d12115b8cd28f0"
	// ClamAV is the malware-scanner image; it ships its signatures, so clamd starts offline.
	ClamAV = "clamav/clamav:1.5.4@sha256:638d90201ee7ee6a82d249a2fc23b132e578673b5f1f2766852b127676ca1ef6"
	// PubSubEmulator is the Google Cloud Pub/Sub emulator integration-test image.
	PubSubEmulator = "gcr.io/google.com/cloudsdktool/google-cloud-cli:588.0.0-emulators@sha256:be2f0e582a94c9e06e7a384fe2b3b39d590eeb8da65a110eda991598c100fe80"
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
