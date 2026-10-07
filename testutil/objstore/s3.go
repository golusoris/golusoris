// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package objstore boots object-storage emulators via testcontainers-go and
// holds the conformance suite every storage.Bucket backend runs.
//
// Usage:
//
//	srv := objstore.StartS3(t)
//	bucket, _ := storage.NewS3Bucket(ctx, storage.S3Options{
//	    Bucket: srv.Bucket, Region: srv.Region, Endpoint: srv.Endpoint,
//	    AccessKey: srv.AccessKey, SecretKey: srv.SecretKey, PathStyle: true,
//	})
//	objstore.RunConformance(t, bucket, objstore.Capabilities{PresignEnforced: true})
//
// Each Start call boots a fresh container. Docker is required; under -short,
// or when Docker is unavailable, the calling test is skipped.
package objstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/internal/testimages"
	"github.com/golusoris/golusoris/testutil/internal/startgate"
)

const (
	// startTimeout bounds one container start including a cold image pull;
	// same value as testutil/pg (cold ARC runners).
	startTimeout = 3 * time.Minute
	stopTimeout  = 10 * time.Second
	setupTimeout = 30 * time.Second

	s3Port      = "7070/tcp"
	s3Region    = "us-east-1"
	s3AccessKey = "golusoris-test"
	s3SecretKey = "golusoris-test-secret"
	s3Bucket    = "conformance"
)

// S3Server is a running SigV4-enforcing S3 endpoint holding one empty bucket.
// Clients must use path-style addressing.
type S3Server struct {
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	Bucket    string
}

// StartS3 boots a VersityGW posix gateway and creates [S3Server.Bucket].
func StartS3(t *testing.T) S3Server {
	t.Helper()
	endpoint := startContainer(t, "s3", testcontainers.ContainerRequest{
		Image:        testimages.VersityGW,
		ExposedPorts: []string{s3Port},
		Env: map[string]string{
			"ROOT_ACCESS_KEY": s3AccessKey,
			"ROOT_SECRET_KEY": s3SecretKey,
		},
		Cmd:        []string{"posix", "/tmp/vgw"},
		WaitingFor: wait.ForListeningPort(s3Port),
	}, s3Port)
	srv := S3Server{
		Endpoint:  "http://" + endpoint,
		Region:    s3Region,
		AccessKey: s3AccessKey,
		SecretKey: s3SecretKey,
		Bucket:    s3Bucket,
	}
	ctx, cancel := context.WithTimeout(t.Context(), setupTimeout)
	defer cancel()
	client := s3.New(s3.Options{
		Region:       srv.Region,
		BaseEndpoint: aws.String(srv.Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(srv.AccessKey, srv.SecretKey, ""),
	})
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(srv.Bucket)}); err != nil {
		t.Fatalf("testutil/objstore: create s3 bucket: %v", err)
	}
	return srv
}

// startContainer boots req and returns host:port for the mapped port.
func startContainer(t *testing.T, name string, req testcontainers.ContainerRequest, port string) string {
	t.Helper()
	if testing.Short() {
		t.Skipf("testutil/objstore: %s container-backed; skipped under -short", name)
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	// Queue for a boot slot first, so startTimeout only counts the boot itself.
	defer startgate.Acquire(t)()
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	req.ReaperImage = testimages.Ryuk
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("testutil/objstore: start %s container: %v", name, err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTimeout)
		defer stopCancel()
		if termErr := ctr.Terminate(stopCtx); termErr != nil {
			t.Logf("testutil/objstore: terminate %s container: %v", name, termErr)
		}
	})
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("testutil/objstore: %s host: %v", name, err)
	}
	mapped, err := ctr.MappedPort(ctx, port)
	if err != nil {
		t.Fatalf("testutil/objstore: %s mapped port: %v", name, err)
	}
	return fmt.Sprintf("%s:%s", host, mapped.Port())
}
