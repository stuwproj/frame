# frame

Generic utilities for scanning, checksumming and pooling binary frames.

## Scanning Performance

Below a sample of the benchmarks using 16K frames.

_Note that `frame` scanning is not absolutely zero-allocation as these benchmarks
seem to suggest. These benchmark serve to isolate per scan allocation metrics; under 
real world conditions payload sizes may fluctuate and cause extra allocs.

```
goos: linux
goarch: amd64
pkg: github.com/johnknl/frame
cpu: AMD Ryzen 9 5950X 16-Core Processor
BenchmarkScanner_SeekIndex
BenchmarkScanner_SeekIndex/default
BenchmarkScanner_SeekIndex/default-32              29005             40626 ns/op        13845.83 MB/s      1575348 seeks/s             0 B/op          0 allocs/op
BenchmarkScanner_SeekIndex/with_index
BenchmarkScanner_SeekIndex/with_index-32          107697             11043 ns/op        50938.73 MB/s      5795696 seeks/s             0 B/op          0 allocs/op
BenchmarkScanner_SeekIndex/with_mmap_frame_reader
BenchmarkScanner_SeekIndex/with_mmap_frame_reader-32              639747              1892 ns/op        297306.21 MB/s    33826840 seeks/s             0 B/op          0 allocs/op
BenchmarkScanner_Scan
BenchmarkScanner_Scan/bytes
BenchmarkScanner_Scan/bytes/default/16384
BenchmarkScanner_Scan/bytes/default/16384-32                         507           2352889 ns/op        27227.17 MB/s            1.741 m/records/s             0 B/op          0 allocs/op
BenchmarkScanner_Scan/bytes/with_validation/16384
BenchmarkScanner_Scan/bytes/with_validation/16384-32                 259           4301698 ns/op        14892.38 MB/s            0.9522 m/records/s            0 B/op          0 allocs/op
BenchmarkScanner_Scan/bytes/at_offset/16384
BenchmarkScanner_Scan/bytes/at_offset/16384-32                       495           2211738 ns/op        27154.47 MB/s            1.736 m/records/s             0 B/op          0 allocs/op
BenchmarkScanner_Scan/memory_mapped
BenchmarkScanner_Scan/memory_mapped/default/16384
BenchmarkScanner_Scan/memory_mapped/default/16384-32                 531           2316121 ns/op        27659.39 MB/s            1.768 m/records/s             0 B/op          0 allocs/op
BenchmarkScanner_Scan/memory_mapped/with_validation/16384
BenchmarkScanner_Scan/memory_mapped/with_validation/16384-32         282           4216555 ns/op        15193.09 MB/s            0.9714 m/records/s            0 B/op          0 allocs/op
BenchmarkScanner_Scan/memory_mapped/at_offset/16384
BenchmarkScanner_Scan/memory_mapped/at_offset/16384-32               523           2113208 ns/op        28420.58 MB/s            1.817 m/records/s             0 B/op          0 allocs/op
BenchmarkScanner_Scan/mmap_zero_copy
BenchmarkScanner_Scan/mmap_zero_copy/default/16384
BenchmarkScanner_Scan/mmap_zero_copy/default/16384-32              18302             65648 ns/op        975853.79 MB/s          62.39 m/records/s              0 B/op          0 allocs/op
BenchmarkScanner_Scan/mmap_zero_copy/with_validation/16384
BenchmarkScanner_Scan/mmap_zero_copy/with_validation/16384-32                242           4896834 ns/op        13082.43 MB/s            0.8365 m/records/s            0 B/op          0 allocs/op
BenchmarkScanner_Scan/mmap_zero_copy/at_offset/16384
BenchmarkScanner_Scan/mmap_zero_copy/at_offset/16384-32                    17079             78944 ns/op        760770.33 MB/s          48.64 m/records/s              0 B/op          0 allocs/op
PASS
ok      github.com/johnknl/frame        20.411s
```

### Scanning Large Streams

`frame` is intended for scanning large streams with frames addressed by logical offset (index).

Seeking by index requires reading each consecutive frame header, hence seeking to the nearest offset
before the header at the desired index, can have a large impact on performance when dealing with large
streams. `frame` has built in support for "offset indexes": a mapping of logical offsets to physical
offsets. Populating these indexes is outside of the scope of the library, typically this would be
populated at stream "append time".

## Header

A concrete Header is the way to define a frame format. It is expected to be a fixed-size array of bytes, 
and implement the Header interface which constrains the size to 8, 16, 32, 64, or 128 bytes.

See tests for implementation examples.

### Index Stride

`frame` supports custom strides to leave "gaps" in the sequencing. This can be useful for batching,
embedding logical frames, or striping.

## Examples

<!-- EXAMPLE:ExampleNewPool:start -->
## NewPool

Go reference: [NewPool](https://pkg.go.dev/github.com/johnknl/frame#NewPool).

The following example shows borrowing, copying and returning pooled frames.

```go
pool := frame.NewPool[uint32, exampleHeader](frame.NewDefaultRetentionPolicy(16))

borrowed := pool.Get(5)
borrowed.Set(exampleHeader{}, []byte("hello"))
fmt.Println(len(borrowed.Payload), cap(borrowed.Payload) >= 5)

cloned := pool.Copy(borrowed)
fmt.Println(string(cloned.Payload))

pool.Put(borrowed)
pool.Put(cloned)

again := pool.Get(0)
fmt.Println(len(again.Payload[:0]))
pool.Put(again)

// Output:
// 5 true
// hello
// 0
```
<!-- EXAMPLE:ExampleNewPool:end -->

<!-- EXAMPLE:ExampleCRC32C_Validate:start -->
## CRC32C

### Validate

Go reference: [CRC32C.Validate](https://pkg.go.dev/github.com/johnknl/frame#CRC32C.Validate).

The following example shows checksum validation and corruption detection.

```go
payload := []byte("hello")
var h exampleHeader
binary.BigEndian.PutUint32(h[0:4], uint32(len(payload)))
binary.BigEndian.PutUint16(h[4:6], 1)
binary.BigEndian.PutUint32(h[6:10], 42)
h.setChecksum(frame.Checksum(&h, payload, 0))

crc := &frame.CRC32C[exampleHeader]{}
fmt.Println(crc.Validate(&h, payload) == nil)

payload[0] = 'H'
fmt.Println(errors.Is(crc.Validate(&h, payload), frame.ErrInvalidChecksum))

// Output:
// true
// true
```
<!-- EXAMPLE:ExampleCRC32C_Validate:end -->

<!-- EXAMPLE:ExampleScanner:start -->
## Scanner

Go reference: [Scanner](https://pkg.go.dev/github.com/johnknl/frame#Scanner).

The following example shows scanning a byte stream with checksum validation.

```go
raw := make([]byte, 0, 128)
raw = append(raw, encodeExampleFrame(10, []byte("a"))...)
raw = append(raw, encodeExampleFrame(11, []byte("bc"))...)

s := &frame.Scanner[uint32, exampleHeader]{
	Loader:    &frame.ReaderLoader[uint32, exampleHeader]{Reader: bytes.NewReader(raw)},
	Validator: &frame.CRC32C[exampleHeader]{},
	Limit:     frame.MaxPayloadSize,
}

for s.Scan() {
	f := s.Frame
	fmt.Printf("%d:%s\n", f.Header.Sequence(), string(f.Payload))
}

fmt.Println(errors.Is(s.Err(), io.EOF))

// Output:
// 10:a
// 11:bc
// true
```
<!-- EXAMPLE:ExampleScanner:end -->

<!-- EXAMPLE:ExampleScanner_SeekIndex:start -->
### SeekIndex

Go reference: [Scanner.SeekIndex](https://pkg.go.dev/github.com/johnknl/frame#Scanner.SeekIndex).

The following example shows sparse-index assisted seeks.

```go
raw := make([]byte, 0, 128)
raw = append(raw, encodeExampleFrame(0, []byte("aa"))...)
raw = append(raw, encodeExampleFrame(1, []byte("bb"))...)
raw = append(raw, encodeExampleFrame(2, []byte("cc"))...)

idx := frame.NewIndex()
idx.Set(0, 0)
idx.Set(2, int64((exampleHeaderSize+2)*2))

s := &frame.Scanner[uint32, exampleHeader]{
	Loader: &frame.ReaderLoader[uint32, exampleHeader]{Reader: bytes.NewReader(raw)},
	Index:  idx,
	Limit:  frame.MaxPayloadSize,
}

_ = s.SeekIndex(2)
_ = s.Scan()
f := s.Frame
fmt.Printf("%d:%s\n", f.Header.Sequence(), string(f.Payload))

// Output:
// 2:cc
```
<!-- EXAMPLE:ExampleScanner_SeekIndex:end -->

<!-- EXAMPLE:ExampleScanner_Close:start -->
### Close

Go reference: [Scanner.Close](https://pkg.go.dev/github.com/johnknl/frame#Scanner.Close).

The following example shows explicit scanner cleanup.

```go
raw := encodeExampleFrame(1, []byte("ok"))

s := &frame.Scanner[uint32, exampleHeader]{
	Loader: &frame.ReaderLoader[uint32, exampleHeader]{Reader: bytes.NewReader(raw)},
	Limit:  frame.HeadersOnly,
}

fmt.Println(s.Scan())
fmt.Println(len(s.Frame.Payload))

_ = s.Close()
fmt.Println(s.Frame == nil)

// Output:
// true
// 0
// true
```
<!-- EXAMPLE:ExampleScanner_Close:end -->

## License

MIT
