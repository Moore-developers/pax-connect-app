package historystore

import (
	"math"
	"time"
)

func columnMillisNS(ms int64) int64 {
	// SQLite ranges use nanoseconds, but the old public store interface uses ms.
	// Saturate diagnostic sentinel ranges rather than overflowing multiplication.
	if ms > int64(^uint64(0)>>1)/int64(time.Millisecond) {
		return int64(^uint64(0) >> 1)
	}
	if ms < (-int64(^uint64(0)>>1)-1)/int64(time.Millisecond) {
		return -int64(^uint64(0)>>1) - 1
	}
	return ms * int64(time.Millisecond)
}

func columnMillisEndNS(ms int64) int64 {
	ns := columnMillisNS(ms)
	if ns > math.MaxInt64-999999 {
		return math.MaxInt64
	}
	return ns + 999999
}
