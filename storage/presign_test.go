// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"errors"
	"testing"
	"time"

	"github.com/golusoris/golusoris/storage"
)

func TestValidatePresignTTLBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ttl  time.Duration
		want error
	}{
		{ttl: storage.MinPresignTTL},
		{ttl: storage.MaxPresignTTL},
		{ttl: time.Hour},
		{ttl: 0, want: storage.ErrPresignTTL},
		{ttl: -time.Second, want: storage.ErrPresignTTL},
		{ttl: storage.MinPresignTTL - time.Nanosecond, want: storage.ErrPresignTTL},
		{ttl: storage.MaxPresignTTL + time.Nanosecond, want: storage.ErrPresignTTL},
	} {
		if err := storage.ValidatePresignTTL(tc.ttl); !errors.Is(err, tc.want) {
			t.Errorf("ValidatePresignTTL(%s) = %v, want %v", tc.ttl, err, tc.want)
		}
	}
}

func TestValidatePresignPutOptions(t *testing.T) {
	t.Parallel()
	sha := make([]byte, 32)
	for _, tc := range []struct {
		name    string
		opts    storage.PresignPutOptions
		wantErr bool
	}{
		{name: "zero options", opts: storage.PresignPutOptions{}},
		{name: "all constraints", opts: storage.PresignPutOptions{
			ContentType: "video/mp4", ContentLength: 1, Metadata: map[string]string{"job-id": "42"},
			Checksum: storage.Checksum{Algorithm: storage.ChecksumSHA256, Value: sha},
		}},
		{name: "crc32c", opts: storage.PresignPutOptions{Checksum: storage.Checksum{
			Algorithm: storage.ChecksumCRC32C, Value: make([]byte, 4),
		}}},
		{name: "md5", opts: storage.PresignPutOptions{Checksum: storage.Checksum{
			Algorithm: storage.ChecksumMD5, Value: make([]byte, 16),
		}}},
		{name: "negative length", opts: storage.PresignPutOptions{ContentLength: -1}, wantErr: true},
		{name: "content type newline", opts: storage.PresignPutOptions{ContentType: "text/plain\r\nX: y"}, wantErr: true},
		{name: "metadata name space", opts: storage.PresignPutOptions{Metadata: map[string]string{"a b": "v"}}, wantErr: true},
		{name: "metadata empty name", opts: storage.PresignPutOptions{Metadata: map[string]string{"": "v"}}, wantErr: true},
		{name: "metadata value newline", opts: storage.PresignPutOptions{Metadata: map[string]string{"k": "a\nb"}}, wantErr: true},
		{name: "short sha256", opts: storage.PresignPutOptions{Checksum: storage.Checksum{
			Algorithm: storage.ChecksumSHA256, Value: sha[:31],
		}}, wantErr: true},
		{name: "value without algorithm", opts: storage.PresignPutOptions{Checksum: storage.Checksum{Value: sha}}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := storage.ValidatePresignPut(time.Minute, tc.opts)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidatePresignPut() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidatePresignPutRejectsUnknownChecksum(t *testing.T) {
	t.Parallel()
	err := storage.ValidatePresignPut(time.Minute, storage.PresignPutOptions{
		Checksum: storage.Checksum{Algorithm: "whirlpool", Value: []byte{1}},
	})
	if !errors.Is(err, storage.ErrUnsupportedChecksum) {
		t.Fatalf("ValidatePresignPut() = %v, want ErrUnsupportedChecksum", err)
	}
	if err = storage.ValidatePresignPut(0, storage.PresignPutOptions{}); !errors.Is(err, storage.ErrPresignTTL) {
		t.Fatalf("ValidatePresignPut(ttl 0) = %v, want ErrPresignTTL", err)
	}
}

func TestChecksumBase64(t *testing.T) {
	t.Parallel()
	got := storage.Checksum{Algorithm: storage.ChecksumCRC32C, Value: []byte{0xde, 0xad, 0xbe, 0xef}}.Base64()
	if got != "3q2+7w==" {
		t.Fatalf("Base64() = %q", got)
	}
}
