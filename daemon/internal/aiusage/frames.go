package aiusage

import (
	"bufio"
	"io"

	"github.com/klauspost/compress/zstd"
)

// readFrame consumes exactly one complete compressed frame, leaving a partial
// tail uncommitted. Blocks are delimited by Zstandard's 3-byte block headers.
func readFrame(r *bufio.Reader) ([]byte, error) {
	peek, e := r.Peek(18)
	if e != nil && e != io.EOF && e != bufio.ErrBufferFull {
		return nil, e
	}
	if len(peek) == 0 {
		return nil, io.EOF
	}
	var h zstd.Header
	if e = h.Decode(peek); e != nil {
		return nil, io.ErrUnexpectedEOF
	}
	raw := make([]byte, h.HeaderSize)
	if _, e = io.ReadFull(r, raw); e != nil {
		return nil, e
	}
	read := func(n int) error {
		start := len(raw)
		raw = append(raw, make([]byte, n)...)
		_, e := io.ReadFull(r, raw[start:])
		return e
	}
	if h.Skippable {
		if h.SkippableSize > 64<<20 {
			return nil, ErrUnavailable
		}
		e = read(int(h.SkippableSize))
		return raw, e
	}
	for {
		start := len(raw)
		if e = read(3); e != nil {
			return nil, e
		}
		v := uint32(raw[start]) | uint32(raw[start+1])<<8 | uint32(raw[start+2])<<16
		size := int(v >> 3)
		kind := (v >> 1) & 3
		if kind == 3 {
			return nil, ErrUnavailable
		}
		if kind == 1 {
			size = 1
		}
		if len(raw)+size > 64<<20 {
			return nil, ErrUnavailable
		}
		if e = read(size); e != nil {
			return nil, e
		}
		if v&1 != 0 {
			break
		}
	}
	if h.HasCheckSum {
		if e = read(4); e != nil {
			return nil, e
		}
	}
	return raw, nil
}
