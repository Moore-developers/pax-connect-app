// Package historystore 是历史数据的 SQLite I/O 壳：采集日志（journal 小时文件）、
// 日志库 logs.db 与流量库 traffic.db 的打开、schema 版本、行级写入、分批清理、
// 容量淘汰和损坏处理（history-storage §4–§7、§10）。
//
// 负责什么：每个库一条写连接加最多 4 条只读连接；文件 0600、目录 0700、拒绝符号链接、
// 校验属主；完整性检查失败时改名保留并重建。
// 不负责什么：不解析观测、不做加工与查询映射、不 import proto / controller / sing-box；
// 迁移 schema 版本不在本包范围，版本不符时返回 ErrSchemaVersion（journal 则另起新文件）。
// 并发：每个库的写入只应由一个 owner 协程发起；读连接池可被多个协程并发使用，
// 且不会被写事务阻塞（WAL）。依赖方向：controller → historystore → storage。
package historystore

import "errors"

var (
	// ErrUnsafeFile means a history file is a symlink, not a regular file, or has the wrong owner or mode.
	ErrUnsafeFile = errors.New("historystore: unsafe file")
	// ErrSchemaVersion means a logs.db or traffic.db file carries an unsupported schema version.
	ErrSchemaVersion = errors.New("historystore: schema version mismatch")
	// ErrInvalidLimits means a batch limit was not positive.
	ErrInvalidLimits = errors.New("historystore: invalid limits")
	// ErrCursorRange means a cursor sequence cannot be stored in SQLite's signed integer.
	ErrCursorRange = errors.New("historystore: cursor sequence out of range")
)

// Recovered reports that an unusable file was renamed aside and replaced by a fresh one (HS-R6).
// The zero value means nothing was recovered.
type Recovered struct {
	// Quarantined is the new name of the renamed file; empty when nothing happened.
	Quarantined string
}

// Happened reports whether a file was quarantined.
func (r Recovered) Happened() bool { return r.Quarantined != "" }
