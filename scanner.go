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
	"fmt"
	"io"
)

const (
	// MaxPayloadSize means "no read limit"
	MaxPayloadSize = ^uint32(0)

	// HeadersOnly means "read only the header"
	HeadersOnly = 0

	// DefaultPayloadSize is the allocation of the stratch payload
	// When a pool is used, the pool retention policy default
	// will be used instead.
	DefaultPayloadSize = 16 << 10 // 16 KiB
)

// Scanner scans frames sequentially incrementing the logical index by the stride of each frame.
type Scanner[LT uint16 | uint32, HT ScannerHeader[LT]] struct {
	err            error
	Loader         Loader[LT, HT]
	Validator      Validator[LT, HT]
	Header         HT
	Frame          *Frame[LT, HT]
	Index          *Index
	NextOffset     int64
	LastOffset     int64
	headerSize     int
	Limit          uint32
	LastIndex      uint32
	NextIndex      uint32
	inited         bool
	skipHeaderScan bool
}

// Scan advances the scanner to the next frame.
func (s *Scanner[LT, HT]) Scan() bool {
	if !s.inited {
		if err := s.Init(); err != nil {
			s.err = err
			return false
		}
	}

	if s.err != nil {
		return false
	}

	s.LastOffset = s.NextOffset

	if !s.skipHeaderScan {
		if _, err := s.readHeader(); err != nil {
			s.err = err
			return false
		}
	}

	stride := uint32(s.Header.IndexStride())

	if _, err := s.readPayload(); err != nil {
		s.err = err
		return false
	}

	s.LastIndex = s.NextIndex
	s.NextIndex += stride
	s.NextOffset += int64(s.headerSize) + int64(s.Header.PayloadLength())

	return true
}

// SeekIndex seeks the scanner to the frame with the given index.
// Note that this will consume the header of the frame at the
// target index, but the payload will not be read until the next Scan() call.
// LastOffset() and NextOffset() return positions of the start of headers,
// and may not always correspond to the offset of the reader.
// When reading/validating the header fails, the scanner will be left in an error state,
// Scan() will return false, and Err() will return the same error as SeekIndex().
func (s *Scanner[LT, HT]) SeekIndex(target uint32) error {
	if !s.inited {
		if err := s.Init(); err != nil {
			return err
		}
	}

	if s.err != nil {
		return s.err
	}

	if target == s.NextIndex {
		return nil
	}

	if target < s.NextIndex {
		if _, err := s.Loader.Seek(0, io.SeekStart); err != nil {
			return s.error(err)
		}

		// s.Header = *new(HT)
		s.NextIndex = 0
		s.LastIndex = 0
	}

	if s.Index != nil {
		idx, off, found := s.Index.ForFrameIndex(target)
		if found {
			err := s.Seek(idx, off)
			if err != nil {
				return err
			}

			s.NextIndex = idx
			s.NextOffset = off
		}
	}

	var (
		n   int
		err error
	)

	nextIdx := s.NextIndex
	lastIdx := s.LastIndex

	for {
		if n, err = s.readHeader(); err != nil {
			return s.error(err)
		}

		// Flag to skip the header scan on the next Scan() call,
		// since we already read the header here
		// This will be set to false again after the payload
		// is read in the next Scan() call
		s.skipHeaderScan = true

		stride := uint32(s.Header.IndexStride())
		payloadLen := int64(s.Header.PayloadLength())

		if payloadLen == 0 {
			// validate the header
			if err = s.validate(); err != nil {
				return s.error(err)
			}

			if nextIdx+stride > target {
				s.NextIndex = nextIdx
				s.LastIndex = lastIdx

				return nil
			}

			lastIdx = nextIdx
			nextIdx += stride

			s.LastOffset = s.NextOffset
			s.NextOffset += int64(n)

			continue
		}

		if nextIdx+stride > target {
			s.NextIndex = nextIdx
			s.LastIndex = lastIdx

			return nil
		}

		lastIdx = nextIdx
		nextIdx += stride

		s.LastOffset = s.NextOffset

		if s.NextOffset, err = s.Loader.Seek(payloadLen, io.SeekCurrent); err != nil {
			return s.error(err)
		}
	}
}

// Seek calls Seek() on the underlying reader and updates the scanner's offset
// and index. The caller is responsible for ensuring that the offset and index
// are a valid position in the stream. Next Scan() will attempt to read the frame
// at the provided offset using the provided index for sequencing.
func (s *Scanner[LT, HT]) Seek(idx uint32, offset int64) error {
	if !s.inited {
		if err := s.Init(); err != nil {
			return err
		}
	}

	off, err := s.Loader.Seek(offset, io.SeekStart)
	if err != nil {
		return err
	}

	if off != offset {
		return fmt.Errorf("seek to offset %d returned %d: %w", s.NextOffset, off, io.ErrUnexpectedEOF)
	}

	s.LastIndex = 0
	s.LastOffset = 0

	s.NextOffset = off
	s.NextIndex = idx

	// clear any previous error state, since we have
	// successfully seeked to a new position.
	s.err = nil

	return nil
}

// Close releases scanner-owned scratch payload memory.
// Close() does not close the underlying reader.
func (s *Scanner[LT, HT]) Close() error {
	if s.Frame != nil {
		s.Frame.Payload = nil
		s.Frame = nil
	}

	return nil
}

// Err returns any non-recoverable scanner error.
func (s *Scanner[LT, HT]) Err() error {
	return s.err
}

// Reset the scanner state to the initial state.
func (s *Scanner[LT, HT]) Reset() {
	s.err = nil
	s.LastOffset = 0
	s.NextOffset = 0
	s.LastIndex = 0
	s.NextIndex = 0
	s.skipHeaderScan = false
}

func (s *Scanner[LT, HT]) validate() error {
	if s.Validator != nil {
		var p []byte
		if s.Frame != nil {
			p = s.Frame.Payload
		}

		if err := s.Validator.Validate(&s.Header, p); err != nil {
			return err
		}
	}

	return nil
}

func (s *Scanner[LT, HT]) readHeader() (int, error) {
	return s.Loader.ReadHeader()
}

func (s *Scanner[LT, HT]) readPayload() (n int, err error) {
	s.skipHeaderScan = false
	n, err = s.Loader.ReadPayload()
	if err != nil {
		s.err = err
		return 0, err
	}

	// validate the frame after reading the payload
	if err = s.validate(); err != nil {
		return 0, err
	}

	return n, err
}

// Init initializes the scanner.
// If you do not call Init() explicitly, it will be called automatically on the first Scan() or SeekIndex() call.
func (s *Scanner[LT, HT]) Init() error {
	if s.inited {
		return nil
	}

	s.inited = true

	if s.Frame == nil {
		payloadCap := 0
		if s.Limit != HeadersOnly {
			payloadCap = DefaultPayloadSize
		}

		s.Frame = &Frame[LT, HT]{
			Payload: make([]byte, 0, payloadCap),
		}
	}

	if s.Loader == nil {
		return fmt.Errorf("nil loader")
	}

	s.headerSize = len(s.Header)
	s.Loader.Bind(&s.Header, s.Frame, s.Limit)

	off, err := s.Loader.Seek(s.NextOffset, io.SeekStart)
	if err != nil {
		return err
	}

	if off != s.NextOffset {
		return fmt.Errorf("seek to offset %d returned %d: %w", s.NextOffset, off, io.ErrUnexpectedEOF)
	}

	s.LastOffset = 0

	return nil
}

func (s *Scanner[LT, HT]) error(err error) error {
	s.err = err

	return err
}
