// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager packages/vpkmanager/src/resource_id.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package resource

import (
	"fmt"
	"math"
	"strings"
)

const (
	murmurMultiplier uint32 = 0x5bd1e995
	murmurSeed       uint32 = 0xedabcdef
)

// ResourceID computes the RERL identifier for the exact name that will be
// serialized: a lowercase ASCII, game-relative path with `/` separators and a
// source extension such as `.vmat`, never `.vmat_c`. It does not normalize
// names; callers must use the same canonical path for hashing, bindings and
// RERL serialization.
func ResourceID(path string) (uint64, error) {
	invalid := fmt.Errorf("%w: expected a canonical uncompiled resource path", ErrInvalid)
	if path == "" || len(path) > math.MaxUint32 || !isASCIIPath(path) ||
		strings.HasSuffix(path, "_c") {
		return 0, invalid
	}
	for part := range strings.SplitSeq(path, "/") {
		if part == "" || part == "." || part == ".." {
			return 0, invalid
		}
	}
	filename := path[strings.LastIndexByte(path, '/')+1:]
	name, extension, ok := strings.CutLast(filename, ".")
	if !ok || name == "" || extension == "" {
		return 0, invalid
	}
	return murmurHash64B(path, uint32(len(path))), nil
}

func isASCIIPath(path string) bool {
	for i := 0; i < len(path); i++ {
		b := path[i]
		if b >= 0x80 || b < 0x20 || b == 0x7f || b == '\\' || b == ':' || (b >= 'A' && b <= 'Z') {
			return false
		}
	}
	return true
}

func murmurMix(value uint32) uint32 {
	value *= murmurMultiplier
	return (value ^ (value >> 24)) * murmurMultiplier
}

func murmurHash64B(input string, length uint32) uint64 {
	high := murmurSeed ^ length
	var low uint32
	bytes := []byte(input)
	offset := 0
	for len(bytes)-offset >= 8 {
		first := uint32(bytes[offset]) | uint32(bytes[offset+1])<<8 |
			uint32(bytes[offset+2])<<16 | uint32(bytes[offset+3])<<24
		second := uint32(bytes[offset+4]) | uint32(bytes[offset+5])<<8 |
			uint32(bytes[offset+6])<<16 | uint32(bytes[offset+7])<<24
		high = high*murmurMultiplier ^ murmurMix(first)
		low = low*murmurMultiplier ^ murmurMix(second)
		offset += 8
	}
	tail := bytes[offset:]
	if len(tail) >= 4 {
		first := uint32(tail[0]) | uint32(tail[1])<<8 | uint32(tail[2])<<16 | uint32(tail[3])<<24
		high = high*murmurMultiplier ^ murmurMix(first)
		tail = tail[4:]
	}
	if len(tail) > 0 {
		var packed uint32
		for index, b := range tail {
			packed |= uint32(b) << (index * 8)
		}
		low = (low ^ packed) * murmurMultiplier
	}
	high = (high ^ (low >> 18)) * murmurMultiplier
	low = (low ^ (high >> 22)) * murmurMultiplier
	high = (high ^ (low >> 17)) * murmurMultiplier
	low = (low ^ (high >> 19)) * murmurMultiplier
	return uint64(high)<<32 | uint64(low)
}
