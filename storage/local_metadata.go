// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
)

const (
	// DefaultContentType is applied when PutOptions.ContentType is empty.
	DefaultContentType        = "application/octet-stream"
	maxLocalAttributeBytes    = 16 * 1024
	localAttributeSchema      = 2
	localMetadataPrefix       = ".golusoris-meta-"
	localMetadataSuffix       = ".json"
	localLockName             = ".golusoris-lock"
	localTransactionName      = ".golusoris-transaction.json"
	localStagingDirectoryName = ".golusoris-staging"
)

type localAttributes struct {
	Schema      int               `json:"schema"`
	Key         string            `json:"key"`
	BodySize    int64             `json:"body_size"`
	BodySHA256  string            `json:"body_sha256"`
	ContentType string            `json:"content_type"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func snapshotLocalAttributes(key string, opts PutOptions) localAttributes {
	attrs := localAttributes{
		Schema:      localAttributeSchema,
		Key:         key,
		ContentType: opts.ContentType,
		Metadata:    maps.Clone(opts.Metadata),
	}
	if attrs.ContentType == "" {
		attrs.ContentType = DefaultContentType
	}
	return attrs
}

func encodeLocalAttributes(attrs localAttributes) ([]byte, error) {
	data, err := json.Marshal(attrs)
	if err != nil {
		return nil, fmt.Errorf("storage: encode local attributes: %w", err)
	}
	if len(data) > maxLocalAttributeBytes {
		return nil, fmt.Errorf(
			"storage: local attributes use %d bytes, maximum is %d", len(data), maxLocalAttributeBytes,
		)
	}
	return data, nil
}

func decodeLocalAttributes(data []byte, key string) (localAttributes, error) {
	if len(data) == 0 {
		return defaultLocalAttributes(key), nil
	}
	var attrs localAttributes
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&attrs); err != nil {
		return localAttributes{}, fmt.Errorf("storage: decode local attributes: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return localAttributes{}, errors.New("storage: local attributes contain trailing JSON")
	}
	if attrs.Schema != localAttributeSchema {
		return localAttributes{}, fmt.Errorf("storage: unsupported local attribute schema %d", attrs.Schema)
	}
	if attrs.Key != key {
		return localAttributes{}, fmt.Errorf("storage: local attributes key %q does not match %q", attrs.Key, key)
	}
	if attrs.ContentType == "" {
		return localAttributes{}, errors.New("storage: local attributes omit content type")
	}
	if attrs.BodySize < 0 {
		return localAttributes{}, errors.New("storage: local attributes contain negative body size")
	}
	if len(attrs.BodySHA256) != sha256.Size*2 {
		return localAttributes{}, errors.New("storage: local attributes contain invalid body digest length")
	}
	if _, err := hex.DecodeString(attrs.BodySHA256); err != nil {
		return localAttributes{}, fmt.Errorf("storage: local attributes contain invalid body digest: %w", err)
	}
	attrs.Metadata = maps.Clone(attrs.Metadata)
	return attrs, nil
}

func objectWithAttributes(obj Object, attrs localAttributes) Object {
	obj.ContentType = attrs.ContentType
	obj.Metadata = maps.Clone(attrs.Metadata)
	return obj
}

func defaultLocalAttributes(key string) localAttributes {
	return localAttributes{Schema: localAttributeSchema, Key: key, ContentType: DefaultContentType}
}

func bindLocalAttributes(attrs localAttributes, size int64, digest string) (localAttributes, []byte, error) {
	attrs.BodySize = size
	attrs.BodySHA256 = digest
	data, err := encodeLocalAttributes(attrs)
	if err != nil {
		return localAttributes{}, nil, err
	}
	return attrs, data, nil
}

func localMetadataPath(key, objectName string) string {
	digest := sha256.Sum256([]byte(key))
	name := fmt.Sprintf("%s%x%s", localMetadataPrefix, digest, localMetadataSuffix)
	return filepath.Join(filepath.Dir(objectName), name)
}

func isLocalMetadataName(name string) bool {
	return len(name) == len(localMetadataPrefix)+sha256.Size*2+len(localMetadataSuffix) &&
		len(name) > len(localMetadataPrefix)+len(localMetadataSuffix) &&
		name[:len(localMetadataPrefix)] == localMetadataPrefix &&
		name[len(name)-len(localMetadataSuffix):] == localMetadataSuffix
}

func isLocalControlName(name string) bool {
	return name == localLockName || name == localTransactionName || name == localStagingDirectoryName
}

func stageLocalAttributes(
	ctx context.Context, root *os.Root, data []byte,
) (string, error) {
	if err := checkLocalContext(ctx, "put before metadata stage"); err != nil {
		return "", err
	}
	file, name, err := openLocalTemp(root)
	if err != nil {
		return "", err
	}
	if _, err = file.Write(data); err != nil {
		return "", closeAndRemoveLocalTemp(root, file, name, fmt.Errorf("storage: stage local attributes: %w", err))
	}
	if err = checkLocalContext(ctx, "put after metadata write"); err != nil {
		return "", closeAndRemoveLocalTemp(root, file, name, err)
	}
	if err = file.Sync(); err != nil {
		return "", closeAndRemoveLocalTemp(root, file, name, fmt.Errorf("storage: sync staged local attributes: %w", err))
	}
	if err = file.Close(); err != nil {
		return "", errors.Join(
			fmt.Errorf("storage: close staged local attributes: %w", err),
			removeLocalTemp(root, name),
		)
	}
	return name, nil
}

func readLocalAttributes(
	ctx context.Context, root *os.Root, key, objectName string,
) (localAttributes, error) {
	metadataName := localMetadataPath(key, objectName)
	data, found, err := readBoundedLocalFile(
		ctx, root, metadataName, "local attributes",
	)
	if err != nil {
		return localAttributes{}, err
	}
	if !found {
		return defaultLocalAttributes(key), nil
	}
	return decodeLocalAttributes(data, key)
}

func removeLocalAttributes(root *os.Root, name string) (bool, error) {
	err := root.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: remove local attributes: %w", err)
	}
	return true, nil
}
