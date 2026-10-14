// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hash_test

import (
	"testing"

	"github.com/golusoris/golusoris/hash"
)

var benchSizes = []struct {
	name string
	size int
}{
	{name: "64B", size: 64},
	{name: "4KiB", size: 4 * 1024},
	{name: "1MiB", size: 1024 * 1024},
}

func deterministicBytes(n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte((i * 31) + 17)
	}
	return buf
}

var (
	benchSinkSHA256 string
	benchSinkBLAKE3 string
)

func BenchmarkSHA256(b *testing.B) {
	for _, tc := range benchSizes {
		b.Run(tc.name, func(b *testing.B) {
			data := deterministicBytes(tc.size)
			b.SetBytes(int64(tc.size))
			b.ReportAllocs()
			for b.Loop() {
				benchSinkSHA256 = hash.SHA256(data)
			}
		})
	}
}

func BenchmarkBLAKE3(b *testing.B) {
	for _, tc := range benchSizes {
		b.Run(tc.name, func(b *testing.B) {
			data := deterministicBytes(tc.size)
			b.SetBytes(int64(tc.size))
			b.ReportAllocs()
			for b.Loop() {
				benchSinkBLAKE3 = hash.BLAKE3(data)
			}
		})
	}
}
