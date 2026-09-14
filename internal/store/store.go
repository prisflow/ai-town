// Package store 存档持久化（SQLite，modernc 纯 Go 驱动）。
//
// 文件地图：
//
//	store.go  打开/建表/单槽读写（gzip JSON payload）
//
// 设计：全量存档、单槽覆盖。payload 是大块 gzip JSON（世界 + 村民 + 工作单），
// SQLite 只负责事务、WAL 与文件安全；不做关系化建表——当前没有按字段查询的需求，
// 等事件日志/向量记忆（sqlite-vec）时再扩表。
package store

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"time"
	// 匿名导入，只执行这个包的init()，注册驱动
	_ "modernc.org/sqlite"
)

// schemaVersion 存档库结构版本：不兼容时拒绝打开（宁可报错也不静默改档）。
const schemaVersion = "1"

// DB 存档库句柄（*store.DB 满足 game.Saver 接口）。
type DB struct{ db *sql.DB }

// Open 打开（必要时创建）存档库并初始化 schema。
// DSN 走 WAL + busy_timeout，写入不会被读操作卡死；单连接串行化写。
func Open(path string) (*DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开存档库失败: %w", err)
	}
	sqldb.SetMaxOpenConns(1) // 单写者模型：本进程唯一的写来源
	d := &DB{db: sqldb}
	if err := d.init(); err != nil {
		_ = sqldb.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) init() error {
	if _, err := d.db.Exec(`
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS slot (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  saved_at   TEXT NOT NULL,
  world_name TEXT NOT NULL,
  day        INTEGER NOT NULL,
  payload    BLOB NOT NULL
);`); err != nil {
		return fmt.Errorf("初始化存档表失败: %w", err)
	}
	var v string
	err := d.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v)
	switch {
	case err == sql.ErrNoRows:
		_, err = d.db.Exec(`INSERT INTO meta(key, value) VALUES('schema_version', ?)`, schemaVersion)
		return err
	case err != nil:
		return fmt.Errorf("读取存档版本失败: %w", err)
	case v != schemaVersion:
		return fmt.Errorf("存档库版本不兼容：库为 %s，程序支持 %s", v, schemaVersion)
	}
	return nil
}

// Save 写入存档（覆盖单槽；payload 先 gzip 再入库）。
func (d *DB) Save(payload []byte, worldName string, day int) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		return fmt.Errorf("压缩存档失败: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("压缩存档失败: %w", err)
	}
	_, err := d.db.Exec(`
INSERT INTO slot(id, saved_at, world_name, day, payload) VALUES(1, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  saved_at   = excluded.saved_at,
  world_name = excluded.world_name,
  day        = excluded.day,
  payload    = excluded.payload`,
		time.Now().Format(time.RFC3339), worldName, day, buf.Bytes())
	if err != nil {
		return fmt.Errorf("写入存档失败: %w", err)
	}
	return nil
}

// Load 读取存档并解压；无存档返回 (nil, nil)（game.Saver 的契约：空档不是错误）。
func (d *DB) Load() ([]byte, error) {
	var gz []byte
	err := d.db.QueryRow(`SELECT payload FROM slot WHERE id = 1`).Scan(&gz)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取存档失败: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("存档解压失败: %w", err)
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("存档解压失败: %w", err)
	}
	return b, nil
}

// Info 存档元信息（世界名/天数/保存时间）；无档返回 ok=false。
func (d *DB) Info() (name string, day int, savedAt string, ok bool, err error) {
	row := d.db.QueryRow(`SELECT world_name, day, saved_at FROM slot WHERE id = 1`)
	switch err = row.Scan(&name, &day, &savedAt); err {
	case nil:
		return name, day, savedAt, true, nil
	case sql.ErrNoRows:
		return "", 0, "", false, nil
	default:
		return "", 0, "", false, fmt.Errorf("读取存档信息失败: %w", err)
	}
}

// Close 关闭存档库（WAL 检查点在进程退出时自动完成）。
func (d *DB) Close() error { return d.db.Close() }
