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
//
// An Index has a single owner that mutates and reads it. Reads are not
// synchronized. Other goroutines must not read the index directly; instead
// they obtain an immutable view with Snapshot, which is safe to call
// concurrently with the owner's mutations.
//
// Snapshots share the backing array with the index. Mutations that would
// overwrite entries visible to a snapshot copy the entries first.
type Index struct {
	entries []Entry
	mu      sync.Mutex
	shared  bool
}

// Entry stores one sparse checkpoint.
type Entry struct {
	Index  uint32
	Offset int64
}

// NewIndex creates an offset index
func NewIndex(prealloc int) *Index {
	return &Index{
		entries: make([]Entry, 0, prealloc),
	}
}

// Snapshot returns an immutable view of the current entries.
// It is safe to call concurrently with mutations of the index.
// The returned index must not be mutated.
func (s *Index) Snapshot() *Index {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.entries)
	s.shared = true

	return &Index{
		entries: s.entries[:n:n],
		shared:  true,
	}
}

// Add appends a checkpoint entry for monotonic index streams.
//
// If index is equal to the last entry, it updates that entry in place.
// If index is out of order, it falls back to Set semantics.
func (s *Index) Add(index uint32, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.entries)
	if n == 0 || index > s.entries[n-1].Index {
		// appending never touches entries visible to a snapshot
		s.entries = append(s.entries, Entry{Index: index, Offset: offset})
		return
	}

	if index == s.entries[n-1].Index {
		s.unshareLocked()
		s.entries[n-1].Offset = offset
		return
	}

	s.setLocked(index, offset)
}

// Set inserts or updates the checkpoint for index.
func (s *Index) Set(index uint32, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.setLocked(index, offset)
}

// Reset clears entries.
func (s *Index) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.shared {
		s.entries = make([]Entry, 0, cap(s.entries))
		s.shared = false
		return
	}

	s.entries = s.entries[:0]
}

// TruncateAfter removes all checkpoints after index.
func (s *Index) TruncateAfter(index uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// the first entry after index
	i := sort.Search(len(s.entries), func(i int) bool {
		return s.entries[i].Index > index
	})

	if i == len(s.entries) {
		return
	}

	// shrinking would let later appends overwrite entries visible to a snapshot
	s.unshareLocked()
	s.entries = s.entries[:i]
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

// Entries returns a copy of the entries in the index.
func (s *Index) Entries() []Entry {
	entries := make([]Entry, len(s.entries))
	copy(entries, s.entries)

	return entries
}

// Map returns a map of index to offset.
func (s *Index) Map() map[uint32]int64 {
	m := make(map[uint32]int64, len(s.entries))
	for _, e := range s.entries {
		m[e.Index] = e.Offset
	}

	return m
}

// Has returns true if the checkpoint for index and offset exists.
func (s *Index) Has(index uint32, off int64) bool {
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

func (s *Index) setLocked(index uint32, offset int64) {
	n := len(s.entries)
	if n == 0 || index > s.entries[n-1].Index {
		s.entries = append(s.entries, Entry{Index: index, Offset: offset})
		return
	}

	i := sort.Search(n, func(i int) bool {
		return s.entries[i].Index >= index
	})

	s.unshareLocked()

	if i < n && s.entries[i].Index == index {
		s.entries[i].Offset = offset
		return
	}

	s.entries = append(s.entries, Entry{})
	copy(s.entries[i+1:], s.entries[i:])

	s.entries[i] = Entry{Index: index, Offset: offset}
}

// unshareLocked detaches the entries from any snapshot before an in-place write.
func (s *Index) unshareLocked() {
	if !s.shared {
		return
	}

	entries := make([]Entry, len(s.entries), cap(s.entries))
	copy(entries, s.entries)

	s.entries = entries
	s.shared = false
}

func (s *Index) by(cmp func(i int) bool) (idx uint32, offset int64, found bool) {
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
