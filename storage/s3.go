package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Options configures an [S3Bucket]. It targets any S3-compatible API: real
// AWS S3 (leave Endpoint empty) or MinIO/Ceph/Garage via a custom Endpoint
// plus PathStyle=true.
type S3Options struct {
	// Bucket is the target bucket name. Required.
	Bucket string `koanf:"bucket"`
	// Region is the AWS region (e.g. "us-east-1"). Required; MinIO accepts
	// any non-empty value.
	Region string `koanf:"region"`
	// Endpoint overrides the S3 endpoint URL (e.g. "http://localhost:9000"
	// for MinIO). Empty means real AWS S3.
	Endpoint string `koanf:"endpoint"`
	// AccessKey is the access key ID. When empty, the AWS default credential
	// chain (env, shared config, IAM role) is used.
	AccessKey string `koanf:"access_key"`
	// SecretKey is the secret access key. Paired with AccessKey.
	SecretKey string `koanf:"secret_key"`
	// PathStyle forces path-style addressing (bucket in the path, not the
	// host). Required for MinIO and most non-AWS S3 implementations.
	PathStyle bool `koanf:"path_style"`
	// PresignTTL is how long presigned GET URLs from [S3Bucket.URL] stay
	// valid (default 15m).
	PresignTTL time.Duration `koanf:"presign_ttl"`
}

// s3API is the subset of the s3 client [S3Bucket] depends on. Narrowed to an
// interface so tests can inject a stub without a live endpoint.
type s3API interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// s3Presigner is the subset of the presign client [S3Bucket.URL] depends on.
type s3Presigner interface {
	PresignGetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4PresignedRequest, error)
}

// v4PresignedRequest mirrors the field of s3.PresignedHTTPRequest that we
// consume, kept local so the presigner interface stays test-stubbable.
type v4PresignedRequest = struct {
	URL          string
	Method       string
	SignedHeader map[string][]string
}

const defaultPresignTTL = 15 * time.Minute

// S3Bucket implements [Bucket] against S3-compatible object storage. Safe for
// concurrent use (the underlying s3 client is concurrency-safe).
type S3Bucket struct {
	bucket     string
	client     s3API
	presigner  s3Presigner
	presignTTL time.Duration
}

// NewS3Bucket builds an [S3Bucket] from opts, loading AWS config (and, when
// AccessKey is set, static credentials). For MinIO, set Endpoint and
// PathStyle.
func NewS3Bucket(ctx context.Context, opts S3Options) (*S3Bucket, error) {
	if opts.Bucket == "" {
		return nil, errors.New("storage/s3: bucket is required")
	}
	if opts.Region == "" {
		return nil, errors.New("storage/s3: region is required")
	}

	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(opts.Region)}
	if opts.AccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(opts.AccessKey, opts.SecretKey, ""),
		))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("storage/s3: load aws config: %w", err)
	}

	client := s3.NewFromConfig(cfg, s3ClientOptions(opts)...)
	return &S3Bucket{
		bucket:     opts.Bucket,
		client:     client,
		presigner:  presignAdapter{s3.NewPresignClient(client)},
		presignTTL: presignTTL(opts.PresignTTL),
	}, nil
}

// newS3BucketForTest wires an [S3Bucket] around an injected client/presigner,
// bypassing AWS config loading. Used by hermetic tests.
func newS3BucketForTest(bucket string, client s3API, presigner s3Presigner, ttl time.Duration) *S3Bucket {
	return &S3Bucket{bucket: bucket, client: client, presigner: presigner, presignTTL: presignTTL(ttl)}
}

func presignTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultPresignTTL
	}
	return ttl
}

func s3ClientOptions(opts S3Options) []func(*s3.Options) {
	var fns []func(*s3.Options)
	if opts.Endpoint != "" {
		fns = append(fns, func(o *s3.Options) { o.BaseEndpoint = aws.String(opts.Endpoint) })
	}
	if opts.PathStyle {
		fns = append(fns, func(o *s3.Options) { o.UsePathStyle = true })
	}
	return fns
}

// presignAdapter bridges the concrete *s3.PresignClient to [s3Presigner] so
// the bucket holds a narrow, stub-friendly interface.
type presignAdapter struct{ pc *s3.PresignClient }

func (a presignAdapter) PresignGetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4PresignedRequest, error) {
	req, err := a.pc.PresignGetObject(ctx, in, optFns...)
	if err != nil {
		return nil, fmt.Errorf("storage/s3: presign: %w", err)
	}
	return &v4PresignedRequest{URL: req.URL, Method: req.Method, SignedHeader: req.SignedHeader}, nil
}

// Put implements [Bucket].
func (b *S3Bucket) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Object, error) {
	contentType := opts.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	in := &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        r,
		ContentType: aws.String(contentType),
	}
	if len(opts.Metadata) > 0 {
		in.Metadata = opts.Metadata
	}
	out, err := b.client.PutObject(ctx, in)
	if err != nil {
		return Object{}, fmt.Errorf("storage/s3: put %q: %w", key, err)
	}
	return Object{
		Key:         key,
		ContentType: contentType,
		ETag:        aws.ToString(out.ETag),
	}, nil
}

// Get implements [Bucket]. The caller must close the returned ReadCloser.
func (b *S3Bucket) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, Object{}, ErrNotFound
		}
		return nil, Object{}, fmt.Errorf("storage/s3: get %q: %w", key, err)
	}
	obj := Object{
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
	}
	return out.Body, obj, nil
}

// Delete implements [Bucket]. Deleting a missing key is not an error (S3
// DeleteObject is idempotent).
func (b *S3Bucket) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage/s3: delete %q: %w", key, err)
	}
	return nil
}

// Exists implements [Bucket].
func (b *S3Bucket) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("storage/s3: head %q: %w", key, err)
	}
	return true, nil
}

// List implements [Bucket].
func (b *S3Bucket) List(ctx context.Context, opts ListOptions) ([]Object, error) {
	in := &s3.ListObjectsV2Input{Bucket: aws.String(b.bucket)}
	if opts.Prefix != "" {
		in.Prefix = aws.String(opts.Prefix)
	}
	if opts.Limit > 0 {
		in.MaxKeys = aws.Int32(clampInt32(opts.Limit))
	}

	var out []Object
	for {
		page, err := b.client.ListObjectsV2(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("storage/s3: list %q: %w", opts.Prefix, err)
		}
		for i := range page.Contents {
			c := page.Contents[i]
			out = append(out, Object{
				Key:          aws.ToString(c.Key),
				Size:         aws.ToInt64(c.Size),
				ETag:         aws.ToString(c.ETag),
				LastModified: aws.ToTime(c.LastModified),
			})
			if opts.Limit > 0 && len(out) >= opts.Limit {
				return out, nil
			}
		}
		if !aws.ToBool(page.IsTruncated) || page.NextContinuationToken == nil {
			return out, nil
		}
		in.ContinuationToken = page.NextContinuationToken
	}
}

// URL implements [Bucket] by issuing a presigned GET valid for the configured
// PresignTTL. The object need not exist; the URL is signed, not validated.
func (b *S3Bucket) URL(ctx context.Context, key string) (string, error) {
	req, err := b.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	}, func(po *s3.PresignOptions) { po.Expires = b.presignTTL })
	if err != nil {
		return "", fmt.Errorf("storage/s3: presign %q: %w", key, err)
	}
	return req.URL, nil
}

// clampInt32 caps n to the int32 range for List's MaxKeys, avoiding overflow.
func clampInt32(n int) int32 {
	if n >= math.MaxInt32 {
		return math.MaxInt32
	}
	if n <= 0 {
		return 0
	}
	return int32(n) //nolint:gosec // G115: bounded above by MaxInt32 and below by 0 directly above
}

// isNotFound reports whether err is an S3 "no such key"/404 response. The SDK
// surfaces these as typed *types.NoSuchKey / *types.NotFound, or for HeadObject
// as a generic smithy APIError with a 404 status code.
func isNotFound(err error) bool {
	var noKey *types.NoSuchKey
	var notFound *types.NotFound
	if errors.As(err, &noKey) || errors.As(err, &notFound) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	return false
}

var _ Bucket = (*S3Bucket)(nil)
