package singbox

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
)

// The pinned upstream SRS reader allocates before checking encoded lengths.
// Preflight its supported transport predicates before allowing those allocations.
// Unknown item encodings fail closed; upgrading the kernel requires reviewing this seam.
func boundedRoutingSRS(raw []byte) bool {
	if len(raw) < 5 || raw[3] < 1 || raw[3] > 5 {
		return false
	}
	z, err := zlib.NewReader(bytes.NewReader(raw[4:]))
	if err != nil {
		return false
	}
	defer func() { _ = z.Close() }()
	data, err := io.ReadAll(io.LimitReader(z, 8<<20+1))
	if err != nil || len(data) > 8<<20 {
		return false
	}
	s := srsBounds{r: bytes.NewReader(data)}
	n, ok := s.count(10000)
	if !ok {
		return false
	}
	for range n {
		if !s.rule(0) {
			return false
		}
	}
	return s.r.Len() == 0
}

type srsBounds struct {
	r     *bytes.Reader
	rules int
}

func (s *srsBounds) count(maximum uint64) (uint64, bool) {
	n, err := binary.ReadUvarint(s.r)
	return n, err == nil && n <= maximum
}
func (s *srsBounds) skip(n uint64) bool {
	if n > uint64(s.r.Len()) {
		return false
	}
	_, err := s.r.Seek(int64(n), io.SeekCurrent)
	return err == nil
}
func (s *srsBounds) list(width uint64) bool { n, ok := s.count(131072); return ok && s.skip(n*width) }
func (s *srsBounds) strings() bool {
	n, ok := s.count(20000)
	if !ok {
		return false
	}
	for range n {
		size, ok := s.count(4096)
		if !ok || !s.skip(size) {
			return false
		}
	}
	return true
}
func (s *srsBounds) rule(depth int) bool {
	s.rules++
	if depth > 16 || s.rules > 20000 {
		return false
	}
	kind, err := s.r.ReadByte()
	if err != nil {
		return false
	}
	if kind == 1 {
		mode, e := s.r.ReadByte()
		if e != nil || mode > 1 {
			return false
		}
		n, ok := s.count(10000)
		if !ok {
			return false
		}
		for range n {
			if !s.rule(depth + 1) {
				return false
			}
		}
		return s.skip(1)
	}
	if kind != 0 {
		return false
	}
	for {
		item, err := s.r.ReadByte()
		if err != nil {
			return false
		}
		switch item {
		case 1, 3, 4, 8, 10, 11, 12, 18:
			if !s.strings() {
				return false
			}
		case 7, 9:
			if !s.list(2) {
				return false
			}
		case 2: // domain succinct bitmap: version, leaves, labels bitmap, labels
			if !s.skip(1) || !s.list(8) || !s.list(8) || !s.list(1) {
				return false
			}
		case 5, 6:
			version, e := s.r.ReadByte()
			if e != nil || version != 1 {
				return false
			}
			var count uint64
			if binary.Read(s.r, binary.BigEndian, &count) != nil || count > 20000 {
				return false
			}
			for range count {
				for range 2 {
					n, ok := s.count(16)
					if !ok || (n != 4 && n != 16) || !s.skip(n) {
						return false
					}
				}
			}
		case 255:
			return s.skip(1)
		default:
			return false
		}
	}
}
