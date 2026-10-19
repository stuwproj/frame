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

package frame_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"stuw.net/frame"
)

const exampleHeaderSize = 16

type exampleHeader [exampleHeaderSize]byte

func (h exampleHeader) PayloadLength() uint32 {
	return binary.BigEndian.Uint32(h[0:4])
}

func (h exampleHeader) IndexStride() uint16 {
	return binary.BigEndian.Uint16(h[4:6])
}

func (h exampleHeader) Sequence() uint32 {
	return binary.BigEndian.Uint32(h[6:10])
}

func (h exampleHeader) Checksum() uint32 {
	return binary.BigEndian.Uint32(h[12:16])
}

func (h exampleHeader) ChecksumBytes() int {
	return 12
}

func (h *exampleHeader) setChecksum(sum uint32) {
	binary.BigEndian.PutUint32(h[12:16], sum)
}

func encodeExampleFrame(sequence uint32, payload []byte) []byte {
	var h exampleHeader
	binary.BigEndian.PutUint32(h[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint16(h[4:6], 1)
	binary.BigEndian.PutUint32(h[6:10], sequence)
	h.setChecksum(frame.Checksum(&h, payload, 0))

	raw := make([]byte, 0, exampleHeaderSize+len(payload))
	raw = append(raw, h[:]...)
	raw = append(raw, payload...)

	return raw
}

// ExampleNewPool shows borrowing, copying and returning pooled frames.
func ExampleNewPool() {
	pool := frame.NewPool[uint32, exampleHeader](frame.NewDefaultRetentionPolicy(16))

	borrowed := pool.Get(5)
	borrowed.Set(exampleHeader{}, []byte("hello"))
	fmt.Println(len(borrowed.Payload), cap(borrowed.Payload) >= 5)

	cloned := pool.Copy(borrowed)
	fmt.Println(string(cloned.Payload))

	pool.Put(borrowed)
	pool.Put(cloned)

	again := pool.Get(0)
	fmt.Println(len(again.Payload[:0]))
	pool.Put(again)

	// Output:
	// 5 true
	// hello
	// 0
}

// ExampleCRC32C_Validate shows checksum validation and corruption detection.
func ExampleCRC32C_Validate() {
	payload := []byte("hello")
	var h exampleHeader
	binary.BigEndian.PutUint32(h[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint16(h[4:6], 1)
	binary.BigEndian.PutUint32(h[6:10], 42)
	h.setChecksum(frame.Checksum(&h, payload, 0))

	crc := &frame.CRC32C[exampleHeader]{}
	fmt.Println(crc.Validate(&h, payload) == nil)

	payload[0] = 'H'
	fmt.Println(errors.Is(crc.Validate(&h, payload), frame.ErrInvalidChecksum))

	// Output:
	// true
	// true
}

// ExampleScanner shows scanning a byte stream with checksum validation.
func ExampleScanner() {
	raw := make([]byte, 0, 128)
	raw = append(raw, encodeExampleFrame(10, []byte("a"))...)
	raw = append(raw, encodeExampleFrame(11, []byte("bc"))...)

	s := &frame.Scanner[uint32, exampleHeader]{
		Loader:    &frame.ReaderLoader[uint32, exampleHeader]{Reader: bytes.NewReader(raw)},
		Validator: &frame.CRC32C[exampleHeader]{},
		Limit:     frame.MaxPayloadSize,
	}

	for s.Scan() {
		f := s.Frame
		fmt.Printf("%d:%s\n", f.Header.Sequence(), string(f.Payload))
	}

	fmt.Println(errors.Is(s.Err(), io.EOF))

	// Output:
	// 10:a
	// 11:bc
	// true
}

// ExampleScanner_SeekIndex shows sparse-index assisted seeks.
func ExampleScanner_SeekIndex() {
	raw := make([]byte, 0, 128)
	raw = append(raw, encodeExampleFrame(0, []byte("aa"))...)
	raw = append(raw, encodeExampleFrame(1, []byte("bb"))...)
	raw = append(raw, encodeExampleFrame(2, []byte("cc"))...)

	idx := frame.NewIndex(2)
	idx.Set(0, 0)
	idx.Set(2, int64((exampleHeaderSize+2)*2))

	s := &frame.Scanner[uint32, exampleHeader]{
		Loader: &frame.ReaderLoader[uint32, exampleHeader]{Reader: bytes.NewReader(raw)},
		Index:  idx,
		Limit:  frame.MaxPayloadSize,
	}

	_ = s.SeekIndex(2)
	_ = s.Scan()
	f := s.Frame
	fmt.Printf("%d:%s\n", f.Header.Sequence(), string(f.Payload))

	// Output:
	// 2:cc
}

// ExampleScanner_Close shows explicit scanner cleanup.
func ExampleScanner_Close() {
	raw := encodeExampleFrame(1, []byte("ok"))

	s := &frame.Scanner[uint32, exampleHeader]{
		Loader: &frame.ReaderLoader[uint32, exampleHeader]{Reader: bytes.NewReader(raw)},
		Limit:  frame.HeadersOnly,
	}

	fmt.Println(s.Scan())
	fmt.Println(len(s.Frame.Payload))

	_ = s.Close()
	fmt.Println(s.Frame == nil)

	// Output:
	// true
	// 0
	// true
}

// ExampleOpenMMapRegion shows scanning through a mapped file with SliceLoader.
func ExampleOpenMMapRegion() {
	f, _ := os.CreateTemp("", "frame-example-*.bin")
	defer os.Remove(f.Name()) // nolint: errcheck
	defer f.Close()

	raw := make([]byte, 0, 64)
	raw = append(raw, encodeExampleFrame(7, []byte("hello"))...)
	_, _ = f.Write(raw)

	region, _ := frame.OpenMMapRegion(f)
	defer region.Close()

	s := &frame.Scanner[uint32, exampleHeader]{
		Loader: &frame.SliceLoader[uint32, exampleHeader]{Data: region.Bytes()},
		Limit:  frame.MaxPayloadSize,
	}

	_ = s.Scan()
	fm := s.Frame
	fmt.Printf("%d:%s\n", fm.Header.Sequence(), string(fm.Payload))

	// Output:
	// 7:hello
}
