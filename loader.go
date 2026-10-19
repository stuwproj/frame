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
	"errors"
	"fmt"
	"io"
)

// ErrSeekBackwards is returned when a scanner is asked to seek backwards
// without a ReaderAt or Seeker.
var ErrSeekBackwards = errors.New("stream does not support seeking backwards")

const discardScratchSize = 8192

var discardScratch = make([]byte, discardScratchSize)

// SeekDiscardReader wraps an io.Reader to implement io.Seeker by discarding bytes until the desired offset is reached.
type SeekDiscardReader struct {
	io.Reader
	offset int64
}

// Read implements io.Reader for a SeekDiscardReader, tracking the number of bytes read.
func (d *SeekDiscardReader) Read(p []byte) (int, error) {
	n, err := d.Reader.Read(p)
	d.offset += int64(n)
	return n, err
}

// Seek implements io.Seeker for a Reader by discarding bytes until the desired offset is reached.
func (d *SeekDiscardReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	// noop
	case io.SeekCurrent:
		offset += d.offset
	case io.SeekEnd:
		return 0, ErrSeekBackwards
	default:
		return 0, errors.New("invalid whence")
	}

	if offset < d.offset {
		return 0, ErrSeekBackwards
	}

	discard := int(offset - d.offset)

	if err := discardN(d.Reader, discard); err != nil {
		return 0, err
	}

	d.offset = offset

	return d.offset, nil

}

// discardN is a more efficient alternative to io.CopyN(io.Discard, r, n)
func discardN(r io.Reader, n int) error {
	if n <= 0 {
		return nil
	}

	remaining := n
	for remaining > 0 {
		chunk := min(discardScratchSize, remaining)
		nD, err := r.Read(discardScratch[:chunk])
		if err != nil {
			return err
		}

		remaining -= nD
	}

	return nil
}

// Loader loads frame header and payload bytes from the underlying source.
// Scanner implementations can swap this dependency to change how bytes are
// materialized without changing scanner state machine logic.
type Loader[LT uint16 | uint32, HT PayloadHeader[LT]] interface {
	ReadHeader() (int, error)
	ReadPayload() (int, error)
	Seek(offset int64, whence int) (int64, error)
	Bind(h *HT, f *Frame[LT, HT], limit uint32)
}

// ReaderLoader loads header/payload using io.ReadSeeker and stores payload
// bytes in the provided scratch frame.
type ReaderLoader[LT uint16 | uint32, HT PayloadHeader[LT]] struct {
	Reader io.ReadSeeker

	h          *HT
	f          *Frame[LT, HT]
	headerView []byte
	headerSize int
	limit      uint32
}

// Bind updates the header/frame/limit pointers used by this frame reader.
func (s *ReaderLoader[LT, HT]) Bind(h *HT, f *Frame[LT, HT], limit uint32) {
	s.h = h
	s.f = f
	s.limit = limit
	s.headerSize = len(*h)
	s.headerView = HeaderBytes(h)
}

// Seek delegates to the bound reader.
func (s *ReaderLoader[LT, HT]) Seek(offset int64, whence int) (int64, error) {
	return s.Reader.Seek(offset, whence)
}

// ReadHeader reads exactly one header into the bound header value.
func (s *ReaderLoader[LT, HT]) ReadHeader() (int, error) {
	n, err := io.ReadFull(s.Reader, s.headerView)
	if err != nil {
		return 0, err
	}

	if n != s.headerSize {
		return 0, io.ErrUnexpectedEOF
	}

	return n, nil
}

// ReadPayload reads payload bytes into the bound frame using configured limit.
func (s *ReaderLoader[LT, HT]) ReadPayload() (n int, err error) {
	s.f.Header = *s.h

	payloadLen := uint32(s.f.Header.PayloadLength())
	n = int(min(payloadLen, s.limit))

	if n == 0 {
		s.f.Payload = s.f.Payload[:0]
		_, err = s.Reader.Seek(int64(payloadLen), io.SeekCurrent)

		return 0, err
	}

	if cap(s.f.Payload) < n || (n > 1024 && cap(s.f.Payload) > n<<4) {
		s.f.Payload = make([]byte, n)
	}

	s.f.Payload = s.f.Payload[:n]

	n, err = io.ReadFull(s.Reader, s.f.Payload)
	if err != nil {
		return 0, err
	}

	if s.limit < payloadLen {
		_, err = s.Reader.Seek(int64(payloadLen)-int64(n), io.SeekCurrent)
	}

	return n, err
}

// SliceLoader provides zero-copy payload reads over mapped bytes.
type SliceLoader[LT uint16 | uint32, HT PayloadHeader[LT]] struct {
	h          *HT
	f          *Frame[LT, HT]
	Data       []byte
	headerView []byte
	off        int64
	headerSize int
	limit      uint32
	// Copy copies payload bytes into Frame.Payload instead of re-slicing Data.
	Copy bool
}

// Bind updates the header/frame/limit pointers used by this loader.
func (s *SliceLoader[LT, HT]) Bind(h *HT, f *Frame[LT, HT], limit uint32) {
	s.h = h
	s.f = f
	s.limit = limit
	s.headerSize = len(*h)
	s.headerView = HeaderBytes(h)
}

// Seek updates the current read offset.
func (s *SliceLoader[LT, HT]) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = s.off + offset
	case io.SeekEnd:
		abs = int64(len(s.Data)) + offset
	default:
		return 0, errors.New("invalid whence")
	}

	if abs < 0 {
		return 0, errors.New("negative position")
	}

	s.off = abs

	return abs, nil
}

// ReadHeader reads one header into the bound header value.
func (s *SliceLoader[LT, HT]) ReadHeader() (int, error) {
	if s.off >= int64(len(s.Data)) {
		return 0, io.EOF
	}

	end := s.off + int64(s.headerSize)
	if end > int64(len(s.Data)) {
		return 0, io.ErrUnexpectedEOF
	}

	copy(s.headerView, s.Data[s.off:end])
	s.off = end

	return s.headerSize, nil
}

// ReadPayload returns payload bytes, aliasing mapped memory when possible.
func (s *SliceLoader[LT, HT]) ReadPayload() (int, error) {
	s.f.Header = *s.h

	payloadLen := uint32(s.f.Header.PayloadLength())
	n := int(min(payloadLen, s.limit))

	endFull := s.off + int64(payloadLen)
	if endFull > int64(len(s.Data)) {
		if s.limit == 0 {
			return 0, io.EOF
		}

		return 0, io.ErrUnexpectedEOF
	}

	if n == 0 {
		s.f.Payload = s.f.Payload[:0]
		s.off = endFull
		return 0, nil
	}

	endRead := s.off + int64(n)
	if endRead > int64(len(s.Data)) {
		return 0, fmt.Errorf("mmap payload window out of bounds")
	}

	if s.Copy {
		if cap(s.f.Payload) < n || (n > 1024 && cap(s.f.Payload) > n<<4) {
			s.f.Payload = make([]byte, n)
		}

		s.f.Payload = s.f.Payload[:n]
		copy(s.f.Payload, s.Data[s.off:endRead])
	} else {
		s.f.Payload = s.Data[s.off:endRead]
	}

	s.off = endFull

	return n, nil
}
