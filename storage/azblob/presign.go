// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/golusoris/golusoris/storage"
)

const (
	// signTimeout bounds the Get User Delegation Key request per URL.
	signTimeout = 30 * time.Second
	// delegationSkew backdates user delegation keys against clock skew.
	delegationSkew = time.Minute
)

// URL implements [storage.Bucket] with a read-only SAS valid for PresignTTL.
func (b *Bucket) URL(ctx context.Context, key string) (string, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return "", err
	}
	signed, _, err := b.sign(ctx, clean, b.presignTTL, sas.BlobPermissions{Read: true})
	return signed, err
}

// PresignPut implements [storage.PutPresigner] with a create+write SAS
// scoped to key. Azure enforces key, expiry, and permissions; MD5 is enforced
// through Content-MD5. Content type and metadata are headers the client
// sends and are applied, not enforced. A SAS cannot bind upload length or a
// CRC32C/SHA-256 digest: those return [storage.ErrUnsupportedConstraint] and
// [storage.ErrUnsupportedChecksum].
func (b *Bucket) PresignPut(
	ctx context.Context, key string, ttl time.Duration, opts storage.PresignPutOptions,
) (storage.PresignedRequest, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return storage.PresignedRequest{}, err
	}
	if err = storage.ValidatePresignPut(ttl, opts); err != nil {
		return storage.PresignedRequest{}, fmt.Errorf("storage/azblob: presign put %q: %w", clean, err)
	}
	header, err := putHeaders(opts)
	if err != nil {
		return storage.PresignedRequest{}, fmt.Errorf("storage/azblob: presign put %q: %w", clean, err)
	}
	signed, expires, err := b.sign(ctx, clean, ttl, sas.BlobPermissions{Create: true, Write: true})
	if err != nil {
		return storage.PresignedRequest{}, err
	}
	return storage.PresignedRequest{Method: http.MethodPut, URL: signed, Header: header, Expires: expires}, nil
}

func putHeaders(opts storage.PresignPutOptions) (http.Header, error) {
	if opts.ContentLength > 0 {
		return nil, fmt.Errorf("%w: SAS cannot bind upload length", storage.ErrUnsupportedConstraint)
	}
	header := http.Header{"X-Ms-Blob-Type": {"BlockBlob"}}
	if opts.ContentType != "" {
		header.Set("X-Ms-Blob-Content-Type", opts.ContentType)
	}
	for name, value := range opts.Metadata {
		if !validMetadataName(name) {
			return nil, fmt.Errorf("metadata name %q is not a C# identifier", name)
		}
		header.Set("X-Ms-Meta-"+strings.ToLower(name), value)
	}
	switch opts.Checksum.Algorithm {
	case "":
	case storage.ChecksumMD5:
		header.Set("Content-MD5", opts.Checksum.Base64())
	case storage.ChecksumSHA256, storage.ChecksumCRC32C:
		return nil, fmt.Errorf("%w: Azure verifies MD5 only", storage.ErrUnsupportedChecksum)
	default:
		return nil, fmt.Errorf("%w: %q", storage.ErrUnsupportedChecksum, opts.Checksum.Algorithm)
	}
	return header, nil
}

// sign returns a blob SAS URL: shared-key signed when the bucket holds an
// account key, user-delegation signed (Entra ID) otherwise.
func (b *Bucket) sign(
	ctx context.Context, key string, ttl time.Duration, perms sas.BlobPermissions,
) (string, time.Time, error) {
	now := b.clock.Now().UTC()
	expires := sasExpiry(now, ttl)
	values := sas.BlobSignatureValues{
		Protocol:      b.protocol,
		ExpiryTime:    expires,
		Permissions:   perms.String(),
		ContainerName: b.name,
		BlobName:      key,
	}
	var params sas.QueryParameters
	var err error
	if b.sharedKey != nil {
		params, err = values.SignWithSharedKey(b.sharedKey)
	} else {
		params, err = b.signWithDelegation(ctx, values, now)
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("storage/azblob: sign %q: %w", key, err)
	}
	return b.container.NewBlobClient(key).URL() + "?" + params.Encode(), expires, nil
}

func (b *Bucket) signWithDelegation(
	ctx context.Context, values sas.BlobSignatureValues, now time.Time,
) (sas.QueryParameters, error) {
	signCtx, cancel := context.WithTimeout(ctx, signTimeout)
	defer cancel()
	cred, err := b.service.GetUserDelegationCredential(signCtx, service.KeyInfo{
		Start:  new(now.Add(-delegationSkew).Format(sas.TimeFormat)),
		Expiry: new(values.ExpiryTime.Format(sas.TimeFormat)),
	}, nil)
	if err != nil {
		return sas.QueryParameters{}, fmt.Errorf("get user delegation key: %w", err)
	}
	params, err := values.SignWithUserDelegation(cred)
	if err != nil {
		return sas.QueryParameters{}, fmt.Errorf("user delegation sas: %w", err)
	}
	return params, nil
}

// sasExpiry rounds up to the whole second SAS carries, so the URL lives at
// least ttl, but never past MaxPresignTTL (the delegation-key ceiling).
func sasExpiry(now time.Time, ttl time.Duration) time.Time {
	want := now.Add(ttl)
	expiry := want.Truncate(time.Second)
	if expiry.Before(want) {
		expiry = expiry.Add(time.Second)
	}
	if ceiling := now.Add(storage.MaxPresignTTL).Truncate(time.Second); expiry.After(ceiling) {
		return ceiling
	}
	return expiry
}
