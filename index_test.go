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
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	benchFrameSink  uint32
	benchOffsetSink int64
)

func TestOffsetIndex_TruncateAfter(t *testing.T) {
	t.Parallel()

	t.Run("keeps floor entry", func(t *testing.T) {
		t.Parallel()

		var idx Index
		idx.Set(0, 64)
		idx.Set(256, 4096)
		idx.Set(512, 8192)

		idx.TruncateAfter(256)

		frameIdx, offset, ok := idx.ForFrameIndex(256)
		require.True(t, ok)

		require.Equal(t, uint32(256), frameIdx)
		require.Equal(t, int64(4096), offset)
	})
}

func TestOffsetIndex_TruncateBefore(t *testing.T) {
	t.Parallel()

	t.Run("keeps floor entry", func(t *testing.T) {
		t.Parallel()

		var idx Index
		idx.Set(0, 64)
		idx.Set(256, 4096)
		idx.Set(512, 8192)

		idx.TruncateBefore(300)

		frameIdx, offset, ok := idx.ForFrameIndex(300)
		require.True(t, ok)
		require.Equal(t, uint32(256), frameIdx)
		require.Equal(t, int64(4096), offset)

		frameIdx, offset, ok = idx.ForFrameIndex(512)
		require.True(t, ok)
		require.Equal(t, uint32(512), frameIdx)
		require.Equal(t, int64(8192), offset)
	})
}

func TestOffsetIndex_ForOffset(t *testing.T) {
	t.Parallel()

	t.Run("returns floor and reflects Set updates", func(t *testing.T) {
		t.Parallel()

		var idx Index
		idx.Set(256, 5000)
		idx.Set(0, 64)
		idx.Set(512, 9000)
		idx.Set(256, 6000)

		frameIdx, offset, ok := idx.ForOffset(5999)
		require.True(t, ok)
		require.Equal(t, uint32(0), frameIdx)
		require.Equal(t, int64(64), offset)

		frameIdx, offset, ok = idx.ForOffset(6000)
		require.True(t, ok)
		require.Equal(t, uint32(256), frameIdx)
		require.Equal(t, int64(6000), offset)

		frameIdx, offset, ok = idx.ForOffset(10000)
		require.True(t, ok)
		require.Equal(t, uint32(512), frameIdx)
		require.Equal(t, int64(9000), offset)
	})
}

func TestOffsetIndex_Reset(t *testing.T) {
	t.Parallel()

	var idx Index
	idx.Set(0, 64)
	idx.Set(128, 4096)

	idx.Reset()

	_, _, ok := idx.ForFrameIndex(128)
	require.False(t, ok)
}

func TestOffsetIndex_Entries(t *testing.T) {
	t.Parallel()

	var idx Index
	idx.Set(256, 4096)
	idx.Set(0, 64)

	entries := idx.Entries()
	require.Equal(t, []Entry{{Index: 0, Offset: 64}, {Index: 256, Offset: 4096}}, entries)

	entries[0].Offset = 999
	require.True(t, idx.Has(0, 64))
}

func TestOffsetIndex_Map(t *testing.T) {
	t.Parallel()

	var idx Index
	idx.Set(0, 64)
	idx.Set(256, 4096)

	m := idx.Map()
	require.Equal(t, int64(64), m[0])
	require.Equal(t, int64(4096), m[256])

	m[0] = 0
	require.True(t, idx.Has(0, 64))
}

func TestOffsetIndex_Has(t *testing.T) {
	t.Parallel()

	var idx Index
	idx.Set(0, 64)
	idx.Set(256, 4096)

	require.True(t, idx.Has(0, 64))
	require.True(t, idx.Has(256, 4096))
	require.False(t, idx.Has(256, 5000))
	require.False(t, idx.Has(128, 1024))
}

func BenchmarkOffsetIndex(b *testing.B) {
	b.Run("lifecycle", func(b *testing.B) {
		const (
			headerSize      = int64(64)
			recordSize      = int64(64 << 10)
			segmentSize     = int64(4 << 30)
			initialRecords  = uint32(segmentSize / recordSize)
			appendBatchSize = uint32(64)
		)

		b.ReportAllocs()

		var idx Index
		var startFrame uint32

		writeFrame := initialRecords
		startOffset := headerSize
		writeOffset := headerSize + int64(writeFrame)*recordSize

		idx.Set(0, headerSize)
		for i := range initialRecords {
			offset := headerSize + int64(i)*recordSize
			if i%appendBatchSize == 0 {
				idx.Set(i, offset)
			}
		}

		idx.Set(writeFrame, writeOffset)

		// loaded
		b.ResetTimer()

		var fi uint32
		var off int64
		for i := 0; i < b.N; i++ {
			for range appendBatchSize {
				idx.Set(writeFrame, writeOffset)
				writeFrame++
				writeOffset += recordSize
			}

			idx.Set(writeFrame, writeOffset)

			span := writeFrame - startFrame
			targetStep := uint32(i) % (span + 1)
			targetFrame := startFrame + targetStep
			targetOffset := startOffset + int64(targetStep)*recordSize + recordSize/2

			f, o, ok := idx.ForFrameIndex(targetFrame)
			if !ok {
				b.Fatal("missing frame lookup")
			}

			fi ^= f
			off ^= o

			f, o, ok = idx.ForOffset(targetOffset)
			if !ok {
				b.Fatal("missing offset lookup")
			}

			fi ^= f
			off ^= o

			if i > 0 && i%256 == 0 && writeFrame-startFrame > appendBatchSize {
				startFrame += 37
				startOffset = headerSize + int64(startFrame)*recordSize

				idx.TruncateBefore(startFrame)
				idx.Set(0, headerSize)
				idx.Set(startFrame, startOffset)
				idx.Set(writeFrame, writeOffset)
			}

			if i > 0 && i%1024 == 0 && writeFrame-startFrame > appendBatchSize {
				writeFrame -= 91
				writeOffset = headerSize + int64(writeFrame)*recordSize
				idx.TruncateAfter(writeFrame)
				idx.Set(writeFrame, writeOffset)
			}

			if i > 0 && i%4096 == 0 {
				idx.Reset()
				startFrame = 0
				writeFrame = initialRecords
				startOffset = headerSize
				writeOffset = headerSize + int64(writeFrame)*recordSize
				idx.Set(0, headerSize)

				for i := range initialRecords {
					offset := headerSize + int64(i)*recordSize
					idx.Set(i, offset)
				}

				idx.Set(writeFrame, writeOffset)
			}
		}

		benchFrameSink = fi
		benchOffsetSink = off
	})
}
