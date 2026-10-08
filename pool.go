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
	"sync"
)

// NewDefaultRetentionPolicy creates a new retention policy with default values
// based on the given maxRetained size.
func NewDefaultRetentionPolicy(maxRetained int) *RetentionPolicy {
	return &RetentionPolicy{
		Warmup:    3,
		Threshold: 1024,
		Ratio:     0.5,
		Max:       maxRetained,
		Default:   maxRetained / 2,
	}
}

// RetentionPolicy is the retention policy for a pool.
type RetentionPolicy struct {
	Warmup    int
	Threshold int
	Ratio     float64
	Max       int
	Default   int
}

// Retainable returns true if the capacity is reusable for the need.
func (p *RetentionPolicy) Retainable(capacity int) bool {
	return capacity <= p.Max
}

// Reusable returns true if the capacity is reusable for the need.
func (p *RetentionPolicy) Reusable(capacity, need int) bool {
	if capacity < need {
		return false
	}

	if capacity-need <= p.Threshold {
		return true
	}

	return float64(capacity-need)/float64(capacity) <= p.Ratio
}

// Pool is a pool of borrowed values that can be reused to avoid allocations.
type Pool[LT uint16 | uint32, HT PayloadHeader[LT]] struct {
	pol  *RetentionPolicy
	pool sync.Pool
}

// NewPool creates a new pool of borrowed values with exponential payload size buckets.
// Buckets start at defaultSize and grow by powers of two up to maxRetainedBucketSize.
func NewPool[LT uint16 | uint32, HT PayloadHeader[LT]](pol *RetentionPolicy) *Pool[LT, HT] {
	p := &Pool[LT, HT]{
		pol: pol,
	}

	p.pool = sync.Pool{
		New: func() any {
			return &Frame[LT, HT]{
				Payload: make([]byte, 0, pol.Default),
			}
		},
	}

	for range pol.Warmup {
		p.pool.Put(p.pool.New())
	}

	return p
}

// Get returns a borrowed value from the pool.
// The value should be returned to the pool with Put when no longer needed.
func (p *Pool[LT, HT]) Get(n int) *Frame[LT, HT] {
	if n == 0 {
		n = p.pol.Default
	}

	f := p.pool.Get().(*Frame[LT, HT]) // nolint:errcheck // safe
	if !p.pol.Reusable(cap(f.Payload), n) {
		out := &Frame[LT, HT]{
			Payload: make([]byte, n),
		}

		return out
	}

	return f
}

// Copy clones src header and payload into a reusable pooled frame.
// The returned frame should be released with Return when no longer needed.
func (p *Pool[LT, HT]) Copy(src *Frame[LT, HT]) *Frame[LT, HT] {
	f := p.pool.Get().(*Frame[LT, HT]) // nolint:errcheck // safe
	n := len(src.Payload)
	if !p.pol.Reusable(cap(f.Payload), n) {
		out := &Frame[LT, HT]{
			Header:  src.Header,
			Payload: make([]byte, n),
		}
		copy(out.Payload, src.Payload)

		return out
	}

	f.Header = src.Header
	f.Payload = append(f.Payload[:0], src.Payload...)

	return f
}

// Put returns a borrowed value to the pool. The value should not be used after calling Put.
func (p *Pool[LT, HT]) Put(v *Frame[LT, HT]) {
	if !p.pol.Retainable(cap(v.Payload)) {
		return
	}

	p.pool.Put(v)
}
