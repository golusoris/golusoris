// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
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
	// valid (default 15m, at most [MaxPresignTTL]).
	PresignTTL time.Duration `koanf:"presign_ttl"`
	// RoleARN assumes this IAM role on top of the base credentials (static
	// keys or the default chain): STS AssumeRole, or AssumeRoleWithWebIdentity
	// when WebIdentityTokenFile is set.
	RoleARN string `koanf:"role_arn"`
	// WebIdentityTokenFile is an OIDC token file (e.g. a projected Kubernetes
	// service-account token) exchanged for RoleARN credentials. Requires
	// RoleARN; excludes static keys.
	WebIdentityTokenFile string `koanf:"web_identity_token_file"`
	// RoleSessionName names assumed-role sessions (default: SDK-generated).
	RoleSessionName string `koanf:"role_session_name"`
	// STSEndpoint overrides the STS endpoint used for role assumption.
	STSEndpoint string `koanf:"sts_endpoint"`
	// PartSize is the multipart part size in bytes (default 8 MiB, range
	// [S3MinPartSize]..[S3MaxPartSize]). Unknown-length bodies are capped at
	// 10000 parts.
	PartSize int64 `koanf:"part_size"`
	// MultipartThreshold is the body size from which Put switches to
	// multipart upload (default 16 MiB, range S3MinPartSize..S3MaxPartSize).
	MultipartThreshold int64 `koanf:"multipart_threshold"`
	// Concurrency bounds parallel part transfers per call (default 5, at most
	// [S3MaxConcurrency]). Peak Put buffer memory is (Concurrency+1)*PartSize.
	Concurrency int `koanf:"concurrency"`
}

// s3API is the subset of the s3 client [S3Bucket] depends on. Narrowed to an
// interface so tests can inject a stub without a live endpoint.
type s3API interface {
	transfermanager.S3APIClient
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	CopyObject(ctx context.Context, in *s3.CopyObjectInput, optFns ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
	UploadPartCopy(ctx context.Context, in *s3.UploadPartCopyInput, optFns ...func(*s3.Options)) (*s3.UploadPartCopyOutput, error)
}

// s3Presigner is the subset of the presign client [S3Bucket] depends on.
type s3Presigner interface {
	PresignGetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4PresignedRequest, error)
	PresignPutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.PresignOptions)) (*v4PresignedRequest, error)
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
	transfer   s3Transfer
	uploader   *transfermanager.Client
}

// NewS3Bucket builds an [S3Bucket] from opts, loading AWS config (and, when
// AccessKey is set, static credentials; when RoleARN is set, an assumed role).
// For MinIO, set Endpoint and PathStyle.
func NewS3Bucket(ctx context.Context, opts S3Options) (*S3Bucket, error) {
	if opts.Bucket == "" {
		return nil, errors.New("storage/s3: bucket is required")
	}
	if opts.Region == "" {
		return nil, errors.New("storage/s3: region is required")
	}
	if err := validateS3Credentials(opts); err != nil {
		return nil, err
	}
	if opts.PresignTTL > MaxPresignTTL {
		return nil, fmt.Errorf("storage/s3: presign_ttl: %w", ValidatePresignTTL(opts.PresignTTL))
	}
	transfer, err := resolveS3Transfer(opts)
	if err != nil {
		return nil, err
	}
	cfg, err := loadS3Config(ctx, opts)
	if err != nil {
		return nil, err
	}
	transfer.checksums = cfg.RequestChecksumCalculation
	client := s3.NewFromConfig(cfg, s3ClientOptions(opts)...)
	return &S3Bucket{
		bucket:     opts.Bucket,
		client:     client,
		presigner:  presignAdapter{s3.NewPresignClient(client)},
		presignTTL: presignTTL(opts.PresignTTL),
		transfer:   transfer,
		uploader:   newS3Uploader(client, transfer),
	}, nil
}

// newS3BucketForTest wires an [S3Bucket] around an injected client/presigner,
// bypassing AWS config loading. Used by hermetic tests.
func newS3BucketForTest(bucket string, client s3API, presigner s3Presigner, ttl time.Duration) *S3Bucket {
	transfer := defaultS3Transfer()
	return &S3Bucket{
		bucket: bucket, client: client, presigner: presigner, presignTTL: presignTTL(ttl),
		transfer: transfer, uploader: newS3Uploader(client, transfer),
	}
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

func (a presignAdapter) PresignPutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.PresignOptions)) (*v4PresignedRequest, error) {
	req, err := a.pc.PresignPutObject(ctx, in, optFns...)
	if err != nil {
		return nil, fmt.Errorf("storage/s3: presign put: %w", err)
	}
	return &v4PresignedRequest{URL: req.URL, Method: req.Method, SignedHeader: req.SignedHeader}, nil
}

// Put implements [Bucket]. Bodies below MultipartThreshold go up in one
// PutObject; larger or unknown-length bodies stream as a bounded multipart
// upload, aborted on failure.
func (b *S3Bucket) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Object, error) {
	clean, err := cleanS3Key(key)
	if err != nil {
		return Object{}, err
	}
	contentType := opts.ContentType
	if contentType == "" {
		contentType = DefaultContentType
	}
	body, bodySizer, err := newS3Body(ctx.Err, r)
	if err != nil {
		return Object{}, err
	}
	metadata := maps.Clone(opts.Metadata)
	in := &transfermanager.UploadObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(clean),
		Body:        body,
		ContentType: aws.String(contentType),
	}
	if len(metadata) > 0 {
		in.Metadata = metadata
	}
	out, err := b.uploader.UploadObject(ctx, in)
	if err != nil {
		return Object{}, fmt.Errorf("storage/s3: put %q: %w", clean, err)
	}
	size, err := bodySizer.size()
	if err != nil {
		return Object{}, fmt.Errorf("storage/s3: measure put %q: %w", clean, err)
	}
	return Object{
		Key:         clean,
		Size:        size,
		ContentType: contentType,
		Metadata:    maps.Clone(metadata),
		ETag:        aws.ToString(out.ETag),
	}, nil
}

// Get implements [Bucket]. The caller must close the returned ReadCloser.
func (b *S3Bucket) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	clean, err := cleanS3Key(key)
	if err != nil {
		return nil, Object{}, err
	}
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(clean),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, Object{}, ErrNotFound
		}
		return nil, Object{}, fmt.Errorf("storage/s3: get %q: %w", clean, err)
	}
	obj := Object{
		Key:          clean,
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		Metadata:     maps.Clone(out.Metadata),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
	}
	return out.Body, obj, nil
}

// Delete implements [Bucket]. Deleting a missing key is not an error (S3
// DeleteObject is idempotent).
func (b *S3Bucket) Delete(ctx context.Context, key string) error {
	clean, err := cleanS3Key(key)
	if err != nil {
		return err
	}
	_, err = b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(clean),
	})
	if err != nil {
		return fmt.Errorf("storage/s3: delete %q: %w", clean, err)
	}
	return nil
}

// Exists implements [Bucket].
func (b *S3Bucket) Exists(ctx context.Context, key string) (bool, error) {
	clean, err := cleanS3Key(key)
	if err != nil {
		return false, err
	}
	_, err = b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(clean),
	})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("storage/s3: head %q: %w", clean, err)
	}
	return true, nil
}

// Stat implements [Bucket] via HeadObject, returning metadata without the
// body. Maps S3 404 / NotFound to [ErrNotFound].
func (b *S3Bucket) Stat(ctx context.Context, key string) (Object, error) {
	clean, err := cleanS3Key(key)
	if err != nil {
		return Object{}, err
	}
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(clean),
	})
	if err != nil {
		if isNotFound(err) {
			return Object{}, ErrNotFound
		}
		return Object{}, fmt.Errorf("storage/s3: stat %q: %w", clean, err)
	}
	return Object{
		Key:          clean,
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		Metadata:     maps.Clone(out.Metadata),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
	}, nil
}

// List implements [Bucket].
func (b *S3Bucket) List(ctx context.Context, opts ListOptions) ([]Object, error) {
	limit, err := normalizeListLimit(opts.Limit)
	if err != nil {
		return nil, err
	}
	prefix, err := cleanS3Prefix(opts.Prefix)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage/s3: list %q: %w", prefix, err)
	}
	maxKeys := int32(limit) // #nosec G115 -- normalizeListLimit proves the value is within 1..1000.
	in := &s3.ListObjectsV2Input{
		Bucket:  aws.String(b.bucket),
		MaxKeys: aws.Int32(maxKeys),
	}
	if prefix != "" {
		in.Prefix = aws.String(prefix)
	}

	page, err := b.client.ListObjectsV2(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("storage/s3: list %q: %w", prefix, err)
	}
	if err = ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage/s3: list %q: %w", prefix, err)
	}
	count := min(limit, len(page.Contents))
	out := make([]Object, 0, count)
	for i := range count {
		if err = ctx.Err(); err != nil {
			return nil, fmt.Errorf("storage/s3: list %q: %w", prefix, err)
		}
		item := page.Contents[i]
		key, keyErr := cleanS3Key(aws.ToString(item.Key))
		if keyErr != nil {
			return nil, fmt.Errorf("storage/s3: unsafe listed key: %w", keyErr)
		}
		out = append(out, Object{
			Key:          key,
			Size:         aws.ToInt64(item.Size),
			ETag:         aws.ToString(item.ETag),
			LastModified: aws.ToTime(item.LastModified),
		})
	}
	return out, nil
}

// URL implements [Bucket] by issuing a presigned GET valid for the configured
// PresignTTL. The object need not exist; the URL is signed, not validated.
func (b *S3Bucket) URL(ctx context.Context, key string) (string, error) {
	clean, err := cleanS3Key(key)
	if err != nil {
		return "", err
	}
	req, err := b.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(clean),
	}, func(po *s3.PresignOptions) { po.Expires = b.presignTTL })
	if err != nil {
		return "", fmt.Errorf("storage/s3: presign %q: %w", clean, err)
	}
	return req.URL, nil
}

func cleanS3Key(key string) (string, error) {
	clean, err := CleanKey(key, MaxKeyBytes)
	if err != nil {
		return "", fmt.Errorf("storage/s3: validate key: %w", err)
	}
	return clean, nil
}

func cleanS3Prefix(prefix string) (string, error) {
	clean, err := cleanListPrefix(prefix)
	if err != nil {
		return "", fmt.Errorf("storage/s3: validate list prefix: %w", err)
	}
	return clean, nil
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
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	return false
}

var (
	_ Bucket       = (*S3Bucket)(nil)
	_ Copier       = (*S3Bucket)(nil)
	_ PutPresigner = (*S3Bucket)(nil)
)
