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

//go:build linux || darwin

package frame

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrMMapUnsupported indicates mmap is unavailable on this platform.
var ErrMMapUnsupported = errors.New("mmap unsupported on this platform")

type file interface {
	Stat() (os.FileInfo, error)
	Fd() uintptr
	Name() string
}

// MMapRegion is a read-only memory mapped file region.
type MMapRegion struct {
	data []byte
}

// OpenMMapRegion maps filename into memory as read-only bytes.
func OpenMMapRegion(f file) (*MMapRegion, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	size := fi.Size()
	if size == 0 {
		return &MMapRegion{data: make([]byte, 0)}, nil
	}

	if size < 0 {
		return nil, fmt.Errorf("mmap: file %q has negative size", f.Name())
	}

	if size != int64(int(size)) {
		return nil, fmt.Errorf("mmap: file %q is too large", f.Name())
	}

	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}

	return &MMapRegion{data: data}, nil
}

// Bytes returns the mapped bytes.
func (m *MMapRegion) Bytes() []byte {
	if m == nil {
		return nil
	}

	return m.data
}

// Len returns mapped byte length.
func (m *MMapRegion) Len() int {
	if m == nil {
		return 0
	}

	return len(m.data)
}

// Close unmaps the region.
func (m *MMapRegion) Close() error {
	if m == nil {
		return nil
	}
	if m.data == nil {
		return nil
	}

	if len(m.data) == 0 {
		m.data = nil
		return nil
	}

	data := m.data
	m.data = nil

	return syscall.Munmap(data)
}
