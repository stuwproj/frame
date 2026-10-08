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

// Validator validates a decoded frame.
type Validator[LT uint16 | uint32, HT PayloadHeader[LT]] interface {
	// Validate validates the frame header and payload.
	// The validator may be passed a nil payload if only the header was
	// read. Any error returned by Validate will be returned by the scanner.
	Validate(*HT, []byte) error
}

// Frame is a wrapper around a byte slice that is borrowed from a pool.
type Frame[LT uint16 | uint32, HT PayloadHeader[LT]] struct {
	Header  HT
	Payload []byte
}

// Set sets the header and value of the borrowed value.
func (f *Frame[LT, HT]) Set(h HT, p []byte) *Frame[LT, HT] {
	f.Header = h
	f.Payload = append(f.Payload[:0], p...)

	return f
}
