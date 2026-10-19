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
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnknl/frame/internal/testutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/mmap"
)

type mmapTestHeader [16]byte

func (h mmapTestHeader) PayloadLength() uint32 { return binary.BigEndian.Uint32(h[0:4]) }

func TestSliceLoader_ReadPayload(t *testing.T) {
	payload := []byte("hello")
	fr := NewTestFrame(1, payload)
	raw := append(fr.Header[:], fr.Payload...)

	path := filepath.Join(t.TempDir(), "mmap-frame-reader.bin")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	fh, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fh.Close()) })

	region, err := OpenMMapRegion(fh)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, region.Close()) })

	var h mmapTestHeader
	f := &Frame[uint32, mmapTestHeader]{}
	mr := &SliceLoader[uint32, mmapTestHeader]{Data: region.Bytes()}
	mr.Bind(&h, f, 1024)

	n, err := mr.ReadHeader()
	require.NoError(t, err)
	require.Equal(t, 16, n)

	n, err = mr.ReadPayload()
	require.NoError(t, err)
	require.Equal(t, len(payload), n)
	require.Equal(t, payload, f.Payload)

	base := region.Bytes()
	require.True(t, len(base) >= 16+len(payload))
	require.Equal(t, &base[16], &f.Payload[0])
}

func TestSliceLoader_ReadPayload_Copy(t *testing.T) {
	payload := []byte("hello")
	fr := NewTestFrame(1, payload)
	raw := append(fr.Header[:], fr.Payload...)

	var h mmapTestHeader
	f := &Frame[uint32, mmapTestHeader]{}
	mr := &SliceLoader[uint32, mmapTestHeader]{Data: raw, Copy: true}
	mr.Bind(&h, f, 1024)

	_, err := mr.ReadHeader()
	require.NoError(t, err)

	n, err := mr.ReadPayload()
	require.NoError(t, err)
	require.Equal(t, len(payload), n)
	require.Equal(t, payload, f.Payload)

	require.NotSame(t, &raw[16], &f.Payload[0])
	raw[16] = 'H'
	require.Equal(t, []byte("hello"), f.Payload)
}

func TestSliceLoader_ReadPayload_PartialHeaderOnlyReturnsEOF(t *testing.T) {
	payload := []byte("hello")
	fr := NewTestFrame(1, payload)
	raw := append(fr.Header[:], fr.Payload[:2]...)

	var h mmapTestHeader
	f := &Frame[uint32, mmapTestHeader]{}
	mr := &SliceLoader[uint32, mmapTestHeader]{Data: raw}
	mr.Bind(&h, f, 0)

	_, err := mr.ReadHeader()
	require.NoError(t, err)

	_, err = mr.ReadPayload()
	require.ErrorIs(t, err, io.EOF)
}

func TestSliceLoader_ReadPayload_PartialPayloadReadReturnsUnexpectedEOF(t *testing.T) {
	payload := []byte("hello")
	fr := NewTestFrame(1, payload)
	raw := append(fr.Header[:], fr.Payload[:2]...)

	var h mmapTestHeader
	f := &Frame[uint32, mmapTestHeader]{}
	mr := &SliceLoader[uint32, mmapTestHeader]{Data: raw}
	mr.Bind(&h, f, uint32(len(payload)))

	_, err := mr.ReadHeader()
	require.NoError(t, err)

	_, err = mr.ReadPayload()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestSeekDiscardReader_Seek(t *testing.T) {
	t.Run("invalid whence", func(t *testing.T) {
		d := &SeekDiscardReader{Reader: bytes.NewReader([]byte("abc"))}
		_, err := d.Seek(0, 99)
		require.Error(t, err)
	})

	t.Run("seek backwards not supported", func(t *testing.T) {
		d := &SeekDiscardReader{Reader: bytes.NewReader([]byte("abcdef"))}
		_, err := d.Seek(4, io.SeekStart)
		require.NoError(t, err)

		_, err = d.Seek(3, io.SeekStart)
		require.ErrorIs(t, err, ErrSeekBackwards)
	})

	t.Run("seek current positive", func(t *testing.T) {
		d := &SeekDiscardReader{Reader: bytes.NewReader([]byte("abcdef"))}
		off, err := d.Seek(2, io.SeekStart)
		require.NoError(t, err)
		require.Equal(t, int64(2), off)

		off, err = d.Seek(3, io.SeekCurrent)
		require.NoError(t, err)
		require.Equal(t, int64(5), off)
	})

	t.Run("seek beyond available returns EOF", func(t *testing.T) {
		d := &SeekDiscardReader{Reader: bytes.NewReader([]byte("abc"))}
		_, err := d.Seek(4, io.SeekStart)
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("seek end not supported", func(t *testing.T) {
		d := &SeekDiscardReader{Reader: bytes.NewReader([]byte("abc"))}
		_, err := d.Seek(0, io.SeekEnd)
		require.ErrorIs(t, err, ErrSeekBackwards)
	})
}

func TestDiscardN(t *testing.T) {
	t.Run("no-op for zero", func(t *testing.T) {
		require.NoError(t, discardN(bytes.NewReader([]byte("abc")), 0))
	})

	t.Run("no-op for negative", func(t *testing.T) {
		require.NoError(t, discardN(bytes.NewReader([]byte("abc")), -1))
	})

	t.Run("returns reader error", func(t *testing.T) {
		errBoom := errors.New("boom")
		r := errReader{err: errBoom}
		err := discardN(r, 1)
		require.ErrorIs(t, err, errBoom)
	})
}

type errReader struct {
	err error
}

func (r errReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

func TestMMapRegion_Len(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var m *MMapRegion
		require.Equal(t, 0, m.Len())
	})

	t.Run("zero-length file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.bin")
		require.NoError(t, os.WriteFile(path, nil, 0o600))

		fh, err := os.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, fh.Close()) })

		region, err := OpenMMapRegion(fh)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, region.Close()) })

		require.Equal(t, 0, region.Len())
		require.Len(t, region.Bytes(), 0)
	})
}

func TestMMapRegion_Close(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mmap-close.bin")
	require.NoError(t, os.WriteFile(path, []byte("abc"), 0o600))

	fh, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fh.Close()) })

	region, err := OpenMMapRegion(fh)
	require.NoError(t, err)

	require.NoError(t, region.Close())
	require.NoError(t, region.Close())
	require.Nil(t, region.Bytes())
}

func BenchmarkLoader_ReadPayload(b *testing.B) {
	const (
		frameCount  = 4096
		payloadSize = 128
	)

	raw := buildLoaderRawCorpus(frameCount, payloadSize)
	path := filepath.Join(b.TempDir(), "frame-reader-bench.bin")
	err := os.WriteFile(path, raw, 0o600)
	if err != nil {
		b.Fatal(err)
	}

	fh, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = fh.Close() })

	b.Run("copy", func(b *testing.B) {
		benchmarkLoaderCopy(b, fh, frameCount, payloadSize, int64(len(raw)))
	})

	b.Run("mmap_readAt_copy", func(b *testing.B) {
		benchmarkLoaderMMapReadAtCopy(b, path, frameCount, payloadSize, int64(len(raw)))
	})

	b.Run("mmap", func(b *testing.B) {
		region, err := OpenMMapRegion(fh)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = region.Close() })

		benchmarkLoaderMMap(b, region, frameCount, payloadSize, int64(len(raw)), false)
	})

	b.Run("mmap_copy", func(b *testing.B) {
		region, err := OpenMMapRegion(fh)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = region.Close() })

		benchmarkLoaderMMap(b, region, frameCount, payloadSize, int64(len(raw)), true)
	})
}

func benchmarkLoaderCopy(b *testing.B, f *os.File, frameCount int, payloadSize int, bytesPerOp int64) {
	var h mmapTestHeader
	fm := &Frame[uint32, mmapTestHeader]{Payload: make([]byte, 0, payloadSize)}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		r := io.NewSectionReader(f, 0, bytesPerOp)
		fr := ReaderLoader[uint32, mmapTestHeader]{Reader: r}
		fr.Bind(&h, fm, uint32(payloadSize))

		for range frameCount {
			_, err := fr.ReadHeader()
			if err != nil {
				b.Fatal(err)
			}

			_, err = fr.ReadPayload()
			if err != nil {
				b.Fatal(err)
			}
		}
	}

	testutil.ReportDomainMetrics(b, int64(frameCount), bytesPerOp)
}

func benchmarkLoaderMMapReadAtCopy(b *testing.B, path string, frameCount int, payloadSize int, bytesPerOp int64) {
	reader, err := mmap.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = reader.Close() })

	var h mmapTestHeader
	fm := &Frame[uint32, mmapTestHeader]{Payload: make([]byte, 0, payloadSize)}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		r := io.NewSectionReader(reader, 0, bytesPerOp)
		fr := ReaderLoader[uint32, mmapTestHeader]{Reader: r}
		fr.Bind(&h, fm, uint32(payloadSize))

		for range frameCount {
			_, err := fr.ReadHeader()
			if err != nil {
				b.Fatal(err)
			}

			_, err = fr.ReadPayload()
			if err != nil {
				b.Fatal(err)
			}
		}
	}

	testutil.ReportDomainMetrics(b, int64(frameCount), bytesPerOp)
}

func benchmarkLoaderMMap(b *testing.B, region *MMapRegion, frameCount int, payloadSize int, bytesPerOp int64, copyPayload bool) {
	var h mmapTestHeader
	fm := &Frame[uint32, mmapTestHeader]{Payload: make([]byte, 0, payloadSize)}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		fr := &SliceLoader[uint32, mmapTestHeader]{Data: region.Bytes(), Copy: copyPayload}
		fr.Bind(&h, fm, uint32(payloadSize))

		_, err := fr.Seek(0, io.SeekStart)
		if err != nil {
			b.Fatal(err)
		}

		for range frameCount {
			_, err = fr.ReadHeader()
			if err != nil {
				b.Fatal(err)
			}

			_, err = fr.ReadPayload()
			if err != nil {
				b.Fatal(err)
			}
		}
	}

	testutil.ReportDomainMetrics(b, int64(frameCount), bytesPerOp)
}

func buildLoaderRawCorpus(frameCount int, payloadSize int) []byte {
	payload := bytes.Repeat([]byte{'x'}, payloadSize)
	raw := make([]byte, 0, frameCount*(TestHeaderSize+payloadSize))

	for i := range frameCount {
		fr := NewTestFrame(uint32(i), payload)
		raw = append(raw, fr.Header[:]...)
		raw = append(raw, fr.Payload...)
	}

	return raw
}
