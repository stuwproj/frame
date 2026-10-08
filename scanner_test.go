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
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/johnknl/frame/internal/testutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/mmap"
)

type TestScanner = Scanner[uint32, TestHeader]
type TestFrame = Frame[uint32, TestHeader]
type TestReaderLoader = ReaderLoader[uint32, TestHeader]
type TestSliceLoader = SliceLoader[uint32, TestHeader]

func buildScannerRawFrames(payloads [][]byte) []byte {
	total := 0
	for _, payload := range payloads {
		total += TestHeaderSize + len(payload)
	}

	raw := make([]byte, 0, total)
	for i, payload := range payloads {
		f := NewTestFrame(uint32(i+1), payload)
		raw = append(raw, f.Header[:]...)
		raw = append(raw, payload...)
	}

	return raw
}

func TestScanner_Scan(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "scan-ok.bin")
		file, err := os.Create(path)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, file.Close()) })

		payloads := [][]byte{[]byte("a"), []byte("bb"), []byte("ccc")}
		for idx, payload := range payloads {
			fr := NewTestFrame(uint32(idx), payload)
			_, err = file.Write(fr.Header[:])
			require.NoError(t, err)
			_, err = file.Write(payload)
			require.NoError(t, err)
		}

		s := &TestScanner{
			Limit:     1 << 20,
			Loader:    &TestReaderLoader{Reader: io.NewSectionReader(file, 0, 1<<63-1)},
			Validator: &CRC32C[TestHeader]{},
		}

		count := 0
		for s.Scan() {
			fr := s.Frame
			require.Equal(t, uint32(count+1)*2, s.NextIndex)
			require.Equal(t, payloads[count], fr.Payload)
			count++
		}

		require.Equal(t, len(payloads), count)
		require.ErrorIs(t, s.Err(), io.EOF)
	})

	t.Run("with custom start index", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "scan-start-index.bin")
		file, err := os.Create(path)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, file.Close()) })

		frames := []*Frame[uint32, TestHeader]{
			NewTestFrame(10, []byte("x")),
			NewTestFrame(11, []byte("yy")),
		}

		for _, fr := range frames {
			_, err = file.Write(fr.Header[:])
			require.NoError(t, err)
			_, err = file.Write(fr.Payload)
			require.NoError(t, err)
		}

		offset := int64(TestHeaderSize + len(frames[0].Payload))

		s := &TestScanner{
			Loader: &TestReaderLoader{
				Reader: io.NewSectionReader(file, 0, 1<<63-1),
			},
		}

		require.NoError(t, s.Seek(10, offset))

		ok := s.Scan()
		require.NoError(t, s.Err())
		require.True(t, ok)
		require.Equal(t, uint32(12), s.NextIndex)

		require.False(t, s.Scan())
		require.ErrorIs(t, s.Err(), io.EOF)
	})
}

func TestScanner_Seek(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "scan-seek.bin")
	file, err := os.Create(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })

	offsets := make([]int64, 6)

	frames := []*Frame[uint32, TestHeader]{
		NewTestFrame(1, []byte("a")),
		NewTestFrame(2, []byte("bb")),
		NewTestFrame(3, []byte("ccc")),
		NewTestFrame(4, []byte("dddd")),
		NewTestFrame(5, []byte("eeeee")),
		NewTestFrame(6, []byte("ffffff")),
	}

	offset := int64(0)
	for i, fr := range frames {
		_, err = file.Write(fr.Header[:])
		require.NoError(t, err)
		_, err = file.Write(fr.Payload)
		require.NoError(t, err)

		offsets[i] = offset

		offset += int64(TestHeaderSize + len(fr.Payload))
	}

	s := &TestScanner{
		Limit: 1 << 20,
		Loader: &TestReaderLoader{
			Reader: io.NewSectionReader(file, 0, 1<<63-1),
		},
	}

	require.True(t, s.Scan())
	require.Equal(t, uint32(1), s.Frame.Header.Meta())
	require.Equal(t, []byte("a"), s.Frame.Payload)
	require.Equal(t, uint32(0), s.LastIndex)
	require.Equal(t, uint32(2), s.NextIndex)

	// position at 2/3/ccc
	err = s.Seek(2, offsets[2])
	require.NoError(t, err)

	require.NoError(t, s.Err())
	require.True(t, s.Scan())
	require.Equal(t, uint32(3), s.Frame.Header.Meta())
	require.Equal(t, []byte("ccc"), s.Frame.Payload)
	require.Equal(t, uint32(2), s.LastIndex)
	require.Equal(t, uint32(4), s.NextIndex)
}

func TestScanner_Reset(t *testing.T) {
	t.Parallel()

	raw := buildScannerRawFrames([][]byte{[]byte("a"), []byte("bb"), []byte("ccc")})
	s := &TestScanner{
		Loader: &TestReaderLoader{Reader: bytes.NewReader(raw)},
		Limit:  MaxPayloadSize,
	}

	count := 0
	for s.Scan() {
		count++
	}
	require.Equal(t, 3, count)
	require.ErrorIs(t, s.Err(), io.EOF)

	s.Reset()
	require.NoError(t, s.Err())
	require.Equal(t, uint32(0), s.NextIndex)
	require.Equal(t, int64(0), s.NextOffset)

	require.NoError(t, s.Seek(0, 0))
	require.True(t, s.Scan())
	require.Equal(t, uint32(1), s.Frame.Header.Meta())
	require.Equal(t, []byte("a"), s.Frame.Payload)
}

func TestScanner_SeekIndex(t *testing.T) {
	requireFrame := func(t *testing.T, s *TestScanner, number uint32, payload []byte) {
		require.Equal(t, number, s.Frame.Header.Meta())
		require.Equal(t, payload, s.Frame.Payload)
	}

	requireHeaderFrameNumber := func(t *testing.T, s *TestScanner, expected uint32) {
		require.Equal(t, expected, s.Header.Meta(), "header number mismatch")
	}

	requireLastFrameNumber := func(t *testing.T, s *TestScanner, expectedLast uint32) {
		if expectedLast == 0 {
			require.Equal(t, uint32(0), s.LastIndex, "last index mismatch")
			require.Equal(t, uint32(0), s.NextIndex, "next index mismatch")
			return
		}

		if expectedLast == 1 {
			require.Equal(t, uint32(0), s.LastIndex, "last index mismatch")
			require.Equal(t, uint32(2), s.NextIndex, "next index mismatch")
			return
		}

		require.Equal(t, (expectedLast-1)*2, s.LastIndex, "last index mismatch")
		require.Equal(t, expectedLast*2, s.NextIndex, "next index mismatch")
	}

	run := func(t *testing.T, r io.ReadSeeker, canSeekBackwards bool) {
		s := &TestScanner{
			Limit: 1 << 20,
			Loader: &TestReaderLoader{
				Reader: r,
			},
		}

		// before starting header is zero
		require.Equal(t, uint32(0), s.Header.Meta())
		requireHeaderFrameNumber(t, s, 0)
		requireLastFrameNumber(t, s, 0)

		require.NoError(t, s.SeekIndex(0))
		requireHeaderFrameNumber(t, s, 0) // header not advanced
		requireLastFrameNumber(t, s, 0)   // position not advanced

		require.True(t, s.Scan())
		requireHeaderFrameNumber(t, s, 1)  // header advanced
		requireLastFrameNumber(t, s, 1)    // position advanced
		requireFrame(t, s, 1, []byte("a")) // first frame

		require.True(t, s.Scan())
		requireHeaderFrameNumber(t, s, 2)   // header advanced
		requireLastFrameNumber(t, s, 2)     // position advanced
		requireFrame(t, s, 2, []byte("bb")) // second frame

		require.True(t, s.Scan())
		requireHeaderFrameNumber(t, s, 3)    // header advanced
		requireLastFrameNumber(t, s, 3)      // position advanced
		requireFrame(t, s, 3, []byte("ccc")) // third frame

		if !canSeekBackwards {
			// try to seek s.nextIdx-1 (3)
			require.ErrorIs(t, s.SeekIndex(3), ErrSeekBackwards)
			requireHeaderFrameNumber(t, s, 3)    // header not advanced
			requireLastFrameNumber(t, s, 3)      // position not advanced
			requireFrame(t, s, 3, []byte("ccc")) // third frame

			// scanner error state
			require.ErrorIs(t, s.Err(), ErrSeekBackwards)

			// repeated scan should return false
			// and not change position or header
			require.False(t, s.Scan())
			requireHeaderFrameNumber(t, s, 3)    // header not advanced
			requireLastFrameNumber(t, s, 3)      // position not advanced
			requireFrame(t, s, 3, []byte("ccc")) // first frame
		} else {
			// internally Seek() is called
			require.NoError(t, s.SeekIndex(3))

			// no error state
			require.NoError(t, s.Err())

			// frame 2 contains 2-4 so seek to 3 should rewind to frame 2
			requireHeaderFrameNumber(t, s, 2) // header peeked at frame 2
			requireLastFrameNumber(t, s, 1)   // position still at frame 1

			// can continue scanning frame 2
			require.True(t, s.Scan())
			requireHeaderFrameNumber(t, s, 2)   // header still at frame 2
			requireLastFrameNumber(t, s, 2)     // position now also at frame 2
			requireFrame(t, s, 2, []byte("bb")) // second frame
		}
	}

	t.Run("readerAt", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "scan-seek-index.bin")
		file, err := os.Create(path)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, file.Close()) })

		frames := []*Frame[uint32, TestHeader]{
			NewTestFrame(1, []byte("a")),
			NewTestFrame(2, []byte("bb")),
			NewTestFrame(3, []byte("ccc")),
			NewTestFrame(4, []byte("dddd")),
		}

		for _, fr := range frames {
			_, err = file.Write(fr.Header[:])
			require.NoError(t, err)
			_, err = file.Write(fr.Payload)
			require.NoError(t, err)
		}

		run(t, io.NewSectionReader(file, 0, 1<<63-1), true)
	})

	t.Run("sequentialReader", func(t *testing.T) {
		t.Parallel()

		raw := buildScannerRawFrames([][]byte{[]byte("a"), []byte("bb"), []byte("ccc"), []byte("dddd")})
		run(t, &SeekDiscardReader{Reader: bytes.NewReader(raw)}, false)
	})
}

func TestScanner_SeekIndex_Stride1(t *testing.T) {
	t.Parallel()

	frames := []*Frame[uint32, TestHeader]{
		NewTestFrame(1, []byte("a")),
		NewTestFrame(2, []byte("bb")),
		NewTestFrame(3, []byte("ccc")),
	}

	raw := make([]byte, 0, 128)
	for _, fr := range frames {
		binary.BigEndian.PutUint16(fr.Header[4:6], 1)
		raw = append(raw, fr.Header[:]...)
		raw = append(raw, fr.Payload...)
	}

	s := &TestScanner{
		Loader: &TestReaderLoader{Reader: bytes.NewReader(raw)},
		Limit:  1 << 20,
	}

	require.NoError(t, s.SeekIndex(1))
	require.True(t, s.Scan())
	require.Equal(t, uint32(2), s.Frame.Header.Meta())
	require.Equal(t, []byte("bb"), s.Frame.Payload)
	require.Equal(t, uint32(1), s.LastIndex)
	require.Equal(t, uint32(2), s.NextIndex)

	require.NoError(t, s.SeekIndex(2))
	require.True(t, s.Scan())
	require.Equal(t, uint32(3), s.Frame.Header.Meta())
	require.Equal(t, []byte("ccc"), s.Frame.Payload)
	require.Equal(t, uint32(2), s.LastIndex)
	require.Equal(t, uint32(3), s.NextIndex)
}

const scannerFrameCount = 4096
const scannerPayloadSize = 128

func BenchmarkScanner_SeekIndex(b *testing.B) {

	buf := buildFrameCorpusFrom(scannerFrameCount, scannerPayloadSize)

	index := NewIndex()
	targets := make([]uint32, 64)
	for i := range targets {
		idx := uint32((i * scannerFrameCount) / len(targets))
		index.Set(idx, int64(idx)*(TestHeaderSize+scannerPayloadSize))

		targets[i] = uint32((i * scannerFrameCount) / len(targets))
	}

	bytesPerRecord := int64(TestHeaderSize + scannerPayloadSize)
	bytesPerOp := bytesPerRecord * scannerFrameCount
	seeksPerOp := int64(len(targets))
	reader := bytes.NewReader(buf)

	b.Run("default", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		s := &TestScanner{
			Loader: &TestReaderLoader{Reader: reader},
		}

		require.NoError(b, s.Init())

		for b.Loop() {
			b.StopTimer()
			_, err := reader.Seek(0, io.SeekStart)
			require.NoError(b, err)
			b.StartTimer()

			for i := range targets {
				target := targets[i]
				err := s.SeekIndex(target)
				require.NoError(b, err)
			}
		}

		testutil.ReportSeekDomainMetrics(b, seeksPerOp, bytesPerOp)
	})

	b.Run("with index", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		s := &TestScanner{
			Index:  index,
			Loader: &TestReaderLoader{Reader: reader},
		}

		require.NoError(b, s.Init())
		for b.Loop() {
			b.StopTimer()
			_, err := reader.Seek(0, io.SeekStart)
			require.NoError(b, err)
			b.StartTimer()

			for i := range targets {
				target := targets[i]
				err := s.SeekIndex(target)
				require.NoError(b, err)
			}
		}

		testutil.ReportSeekDomainMetrics(b, seeksPerOp, bytesPerOp)
	})

	b.Run("with mmap frame reader", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		path := filepath.Join(b.TempDir(), "seek-index-mmap.bin")
		err := os.WriteFile(path, buf, 0o600)
		require.NoError(b, err)

		file, err := os.Open(path)
		require.NoError(b, err)
		b.Cleanup(func() { require.NoError(b, file.Close()) })

		region, err := OpenMMapRegion(file)
		require.NoError(b, err)
		b.Cleanup(func() { require.NoError(b, region.Close()) })

		fr := &TestSliceLoader{Data: region.Bytes()}
		s := &TestScanner{
			Index:  index,
			Loader: fr,
		}

		require.NoError(b, s.Init())

		for b.Loop() {
			for i := range targets {
				target := targets[i]
				if err := s.SeekIndex(target); err != nil {
					b.Fatalf("failed to seek index %d: %v", target, err)
				}
			}
		}

		testutil.ReportSeekDomainMetrics(b, seeksPerOp, bytesPerOp)
	})
}

func BenchmarkScanner_Scan(b *testing.B) {
	var builders = map[string]func(b *testing.B, frameCount, payloadSize int) io.ReadSeeker{
		"file/readAt":   buildFileReaderAtFrom,
		"bytes":         buildByteReaderFrom,
		"memory_mapped": buildMemoryMappedReaderFrom,
	}

	sizes := []int{0, 512, 1024, 16384, 65536, 131072}

	for name, rf := range builders {
		b.Run(name, func(b *testing.B) {
			for _, size := range sizes {
				b.Run("default/"+strconv.Itoa(size), func(b *testing.B) {
					benchScan(b, rf, size, 0, false)
				})
			}

			for _, size := range sizes {
				b.Run("with_validation/"+strconv.Itoa(size), func(b *testing.B) {
					benchScan(b, rf, size, 0, true)
				})
			}

			for _, size := range sizes {
				b.Run("at_offset/"+strconv.Itoa(size), func(b *testing.B) {
					benchScan(b, rf, size, 256, false)
				})
			}
		})
	}

	b.Run("mmap_zero_copy", func(b *testing.B) {
		for _, size := range sizes {
			b.Run("default/"+strconv.Itoa(size), func(b *testing.B) {
				benchScanMMapZeroCopy(b, size, 0, false)
			})
		}

		for _, size := range sizes {
			b.Run("with_validation/"+strconv.Itoa(size), func(b *testing.B) {
				benchScanMMapZeroCopy(b, size, 0, true)
			})
		}

		for _, size := range sizes {
			b.Run("at_offset/"+strconv.Itoa(size), func(b *testing.B) {
				benchScanMMapZeroCopy(b, size, 256, false)
			})
		}
	})

}

func buildByteReaderFrom(_ *testing.B, frameCount, payloadSize int) io.ReadSeeker {
	buf := buildFrameCorpusFrom(frameCount, payloadSize)
	return bytes.NewReader(buf)
}

func buildFileReaderAtFrom(b *testing.B, frameCount, payloadSize int) io.ReadSeeker {
	file, err := buildFileFrom(b, frameCount, payloadSize)
	if err != nil {
		b.Fatalf("failed to create file: %v", err)
	}

	return io.NewSectionReader(file, 0, 1<<63-1)
}

func buildFileFrom(b *testing.B, frameCount, payloadSize int) (*os.File, error) {
	path := b.TempDir() + "/scanner-bench.bin"
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	b.Cleanup(func() {
		require.NoError(b, file.Close())
		require.NoError(b, os.Remove(path))
	})

	buf := buildFrameCorpusFrom(frameCount, payloadSize)
	_, err = file.Write(buf)
	if err != nil {
		return nil, err
	}

	return file, nil
}

func buildMemoryMappedReaderFrom(b *testing.B, frameCount, payloadSize int) io.ReadSeeker {
	file, err := buildFileFrom(b, frameCount, payloadSize)
	if err != nil {
		b.Fatalf("failed to create file: %v", err)
	}

	reader, err := mmap.Open(file.Name())
	if err != nil {
		log.Fatalf("failed to open file: %v", err)
	}

	b.Cleanup(func() { reader.Close() })

	return io.NewSectionReader(reader, 0, 1<<63-1)
}

func buildFrameCorpusFrom(frameCount, payloadSize int) []byte {
	payload := testutil.BenchmarkPayload(payloadSize)
	buf := make([]byte, frameCount*(TestHeaderSize+payloadSize))
	pos := 0
	for i := range frameCount {
		fr := NewTestFrame(uint32(i), payload)
		for j := range TestHeaderSize {
			buf[pos+j] = fr.Header[j]
		}
		pos += TestHeaderSize
		for j := range payloadSize {
			buf[pos+j] = payload[j]
		}
		pos += payloadSize
	}

	return buf
}

func benchScan(b *testing.B, rf func(b *testing.B, frameCount, payloadSize int) io.ReadSeeker, payloadSize, skip int, validate bool) {
	if skip > scannerFrameCount {
		skip = scannerFrameCount
	}

	startOffset := int64(skip * (TestHeaderSize + payloadSize))
	stream := rf(b, scannerFrameCount, payloadSize)

	framesPerScan := int64(scannerFrameCount - skip)
	bytesPerFrame := int64(TestHeaderSize + payloadSize)

	b.ReportAllocs()
	b.ResetTimer()

	s := &TestScanner{
		Loader: &TestReaderLoader{Reader: stream},
		Limit:  MaxPayloadSize,
		Frame: &TestFrame{
			Payload: make([]byte, 0, payloadSize),
		},
	}

	runScanBench(b, s, skip, startOffset, framesPerScan, bytesPerFrame, validate)
}

func benchScanMMapZeroCopy(b *testing.B, payloadSize, skip int, validate bool) {
	if skip > scannerFrameCount {
		skip = scannerFrameCount
	}

	raw := buildFrameCorpusFrom(scannerFrameCount, payloadSize)
	path := filepath.Join(b.TempDir(), "scan-mmap-zero-copy.bin")
	err := os.WriteFile(path, raw, 0o600)
	if err != nil {
		b.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { require.NoError(b, file.Close()) })

	region, err := OpenMMapRegion(file)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { require.NoError(b, region.Close()) })

	startOffset := int64(skip * (TestHeaderSize + payloadSize))

	framesPerScan := int64(scannerFrameCount - skip)
	bytesPerFrame := int64(TestHeaderSize + payloadSize)

	b.ReportAllocs()
	b.ResetTimer()

	fr := &TestSliceLoader{Data: region.Bytes()}
	s := &TestScanner{
		Loader: fr,
		Limit:  MaxPayloadSize,
	}

	runScanBench(b, s, skip, startOffset, framesPerScan, bytesPerFrame, validate)
}

type countingValidator struct {
	v     *CRC32C[TestHeader]
	count int
}

func (v *countingValidator) Validate(h *TestHeader, payload []byte) error {
	v.count++
	return v.v.Validate(h, payload)
}

func runScanBench(b *testing.B, s *TestScanner, skip int, startOffset int64, framesPerScan, bytesPerFrame int64, validate bool) {
	if validate {
		s.Validator = &countingValidator{v: &CRC32C[TestHeader]{}}
	}

	for b.Loop() {
		b.StopTimer()
		if validate {
			s.Validator.(*countingValidator).count = 0 // nolint: errcheck
		}
		require.NoError(b, s.Init())
		require.NoError(b, s.Seek(uint32(skip), startOffset))
		b.StartTimer()

		var count int64
		for s.Scan() {
			count++
		}

		b.StopTimer()
		require.Equal(b, framesPerScan, count)
		if validate {
			require.Equal(b, framesPerScan, int64(s.Validator.(*countingValidator).count)) // nolint: errcheck
		}
		require.ErrorIs(b, s.Err(), io.EOF)
		b.StartTimer()
	}

	testutil.ReportScannerDomainMetrics(b, framesPerScan, framesPerScan*bytesPerFrame)
}
