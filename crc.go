// MIT License
//
// Copyright (C) 2026 John Kleijn
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE

package frame

import (
	"hash/crc32"
)

// CRC32cTable is the CRC32C (Castagnoli) table used for checksum calculations.
var CRC32cTable = crc32.MakeTable(crc32.Castagnoli)

// UpdateCRC32C updates the CRC32C checksum with the given bytes.
func UpdateCRC32C(sum uint32, b []byte) uint32 {
	return crc32.Update(sum, CRC32cTable, b)
}

// CRC32C validates and computes frame checksums using CRC32C (Castagnoli).
type CRC32C[HT ChecksumHeader] struct{}

// Validate checks if the frame header is valid for the given payload
func (v *CRC32C[HT]) Validate(h *HT, p []byte) error {
	if p == nil {
		// ignore header only validation
		return nil
	}

	if (*h).Checksum() != Checksum(h, p, 0) {
		return ErrInvalidChecksum
	}

	return nil
}

// Checksum updates passed checksum with frame checksum.
func Checksum[HT ChecksumHeader](h *HT, p []byte, sum uint32) uint32 {
	b := HeaderBytes(h)
	sum = crc32.Update(sum, CRC32cTable, b[:(*h).ChecksumBytes()])
	sum = crc32.Update(sum, CRC32cTable, p)
	return sum
}
