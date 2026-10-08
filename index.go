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
	"sort"
	"sync"
)

// Index stores sparse index-to-offset checkpoints.
type Index struct {
	entries []Entry
	mu      sync.RWMutex
}

// NewIndex creates an offset index
func NewIndex() *Index {
	return &Index{}
}

// Entry stores one sparse checkpoint.
type Entry struct {
	Index  uint32
	Offset int64
}

// Entries returns a copy of the entries in the index.
func (s *Index) Entries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]Entry, len(s.entries))
	copy(entries, s.entries)
	return entries
}

// Reset clears entries.
func (s *Index) Reset() {
	s.mu.Lock()
	s.entries = s.entries[:0]
	s.mu.Unlock()
}

// Set inserts or updates the checkpoint for index.
func (s *Index) Set(index uint32, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.entries)
	if n == 0 {
		s.entries = append(s.entries, Entry{Index: index, Offset: offset})
		return
	}

	last := s.entries[n-1]
	if index > last.Index {
		s.entries = append(s.entries, Entry{Index: index, Offset: offset})
		return
	}

	i := sort.Search(n, func(i int) bool {
		return s.entries[i].Index >= index
	})

	if i < n && s.entries[i].Index == index {
		s.entries[i].Offset = offset
		return
	}

	s.entries = append(s.entries, Entry{})
	copy(s.entries[i+1:], s.entries[i:])

	s.entries[i] = Entry{Index: index, Offset: offset}
}

// Map returns a map of index to offset.
func (s *Index) Map() map[uint32]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m := make(map[uint32]int64, len(s.entries))
	for _, e := range s.entries {
		m[e.Index] = e.Offset
	}

	return m
}

// Has returns true if the checkpoint for index and offset exists.
func (s *Index) Has(index uint32, off int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, offset, found := s.by(func(i int) bool {
		return s.entries[i].Index > index
	})

	if !found {
		return false
	}

	return idx == index && offset == off
}

// ForFrameIndex returns the last checkpoint index that is <= index.
func (s *Index) ForFrameIndex(index uint32) (idx uint32, off int64, found bool) {
	return s.by(func(i int) bool {
		return s.entries[i].Index > index
	})
}

// ForOffset return the last checkpoint offset that is <= offset.
func (s *Index) ForOffset(offset int64) (idx uint32, off int64, found bool) {
	return s.by(func(i int) bool {
		return s.entries[i].Offset > offset
	})
}

// TruncateAfter removes all checkpoints after index.
func (s *Index) TruncateAfter(index uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.findTruncIndexIndex(index)
	if i == -1 {
		return
	}

	s.entries = s.entries[:i+1]
}

// TruncateBefore removes all checkpoints before index.
func (s *Index) TruncateBefore(index uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.findTruncIndexIndex(index)
	if i == -1 {
		return
	}

	s.entries = s.entries[i:]
}

func (s *Index) by(cmp func(i int) bool) (idx uint32, offset int64, found bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := len(s.entries)
	if n == 0 {
		return 0, 0, false
	}

	i := sort.Search(n, cmp)

	if i == 0 {
		return 0, 0, false
	}

	e := s.entries[i-1]

	return e.Index, e.Offset, true
}

func (s *Index) findTruncIndexIndex(idx uint32) (entryIndex int) {
	n := len(s.entries)
	if n == 0 {
		return -1
	}

	i := sort.Search(n, func(i int) bool {
		return s.entries[i].Index > idx
	})

	if i == 0 {
		return 0
	}

	return i - 1
}
