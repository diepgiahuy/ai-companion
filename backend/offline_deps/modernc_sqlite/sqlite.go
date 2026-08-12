package sqlite

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

func init() { sql.Register("sqlite", &drv{}) }

type drv struct{}

func (*drv) Open(name string) (driver.Conn, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var db *C.sqlite3
	flags := C.int(C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE | C.SQLITE_OPEN_URI | C.SQLITE_OPEN_FULLMUTEX)
	if rc := C.sqlite3_open_v2(cName, &db, flags, nil); rc != C.SQLITE_OK {
		msg := "sqlite open failed"
		if db != nil {
			msg = C.GoString(C.sqlite3_errmsg(db))
			C.sqlite3_close_v2(db)
		}
		return nil, errors.New(msg)
	}
	return &conn{db: db}, nil
}

type conn struct {
	db     *C.sqlite3
	mu     sync.Mutex
	closed bool
}

func (c *conn) Prepare(q string) (driver.Stmt, error) { return c.prepare(q) }
func (c *conn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.prepare(q)
}
func (c *conn) prepare(q string) (driver.Stmt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("sqlite: closed")
	}
	cq := C.CString(q)
	defer C.free(unsafe.Pointer(cq))
	var s *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(c.db, cq, -1, &s, nil); rc != C.SQLITE_OK {
		return nil, c.err()
	}
	return &stmt{c: c, s: s}, nil
}
func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if rc := C.sqlite3_close_v2(c.db); rc != C.SQLITE_OK {
		return c.err()
	}
	return nil
}
func (c *conn) Begin() (driver.Tx, error) { return c.BeginTx(context.Background(), driver.TxOptions{}) }
func (c *conn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := c.ExecContext(ctx, "BEGIN", nil); err != nil {
		return nil, err
	}
	return &tx{c: c}, nil
}
func (c *conn) Ping(ctx context.Context) error { return ctx.Err() }
func (c *conn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := c.prepare(q)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	vals := named(args)
	return s.(*stmt).Exec(vals)
}
func (c *conn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := c.prepare(q)
	if err != nil {
		return nil, err
	}
	vals := named(args)
	rs, err := s.(*stmt).Query(vals)
	if err != nil {
		s.Close()
		return nil, err
	}
	rs.(*rows).closeStmt = true
	return rs, nil
}
func named(in []driver.NamedValue) []driver.Value {
	out := make([]driver.Value, len(in))
	for i := range in {
		out[i] = in[i].Value
	}
	return out
}
func (c *conn) err() error {
	if c.db == nil {
		return errors.New("sqlite error")
	}
	return errors.New(C.GoString(C.sqlite3_errmsg(c.db)))
}

type tx struct{ c *conn }

func (t *tx) Commit() error { _, e := t.c.ExecContext(context.Background(), "COMMIT", nil); return e }
func (t *tx) Rollback() error {
	_, e := t.c.ExecContext(context.Background(), "ROLLBACK", nil)
	return e
}

type stmt struct {
	c      *conn
	s      *C.sqlite3_stmt
	closed bool
}

func (s *stmt) Close() error {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	C.sqlite3_finalize(s.s)
	return nil
}
func (s *stmt) NumInput() int { return int(C.sqlite3_bind_parameter_count(s.s)) }
func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if err := s.bind(args); err != nil {
		return nil, err
	}
	rc := C.sqlite3_step(s.s)
	for rc == C.SQLITE_ROW {
		rc = C.sqlite3_step(s.s)
	}
	if rc != C.SQLITE_DONE {
		C.sqlite3_reset(s.s)
		return nil, s.c.err()
	}
	id := int64(C.sqlite3_last_insert_rowid(s.c.db))
	n := int64(C.sqlite3_changes(s.c.db))
	C.sqlite3_reset(s.s)
	C.sqlite3_clear_bindings(s.s)
	return result{id, n}, nil
}
func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if err := s.bind(args); err != nil {
		return nil, err
	}
	cols := int(C.sqlite3_column_count(s.s))
	names := make([]string, cols)
	for i := 0; i < cols; i++ {
		names[i] = C.GoString(C.sqlite3_column_name(s.s, C.int(i)))
	}
	return &rows{s: s, names: names}, nil
}
func (s *stmt) bind(args []driver.Value) error {
	C.sqlite3_reset(s.s)
	C.sqlite3_clear_bindings(s.s)
	for i, v := range args {
		idx := C.int(i + 1)
		var rc C.int
		switch x := v.(type) {
		case nil:
			rc = C.sqlite3_bind_null(s.s, idx)
		case int64:
			rc = C.sqlite3_bind_int64(s.s, idx, C.sqlite3_int64(x))
		case float64:
			rc = C.sqlite3_bind_double(s.s, idx, C.double(x))
		case bool:
			if x {
				rc = C.sqlite3_bind_int64(s.s, idx, 1)
			} else {
				rc = C.sqlite3_bind_int64(s.s, idx, 0)
			}
		case []byte:
			if len(x) == 0 {
				rc = C.sqlite3_bind_blob(s.s, idx, nil, 0, C.SQLITE_TRANSIENT)
			} else {
				rc = C.sqlite3_bind_blob(s.s, idx, unsafe.Pointer(&x[0]), C.int(len(x)), C.SQLITE_TRANSIENT)
			}
		case string:
			cs := C.CString(x)
			rc = C.sqlite3_bind_text(s.s, idx, cs, C.int(len(x)), C.SQLITE_TRANSIENT)
			C.free(unsafe.Pointer(cs))
		default:
			return fmt.Errorf("sqlite: unsupported bind %T", v)
		}
		if rc != C.SQLITE_OK {
			return s.c.err()
		}
	}
	return nil
}

type rows struct {
	s         *stmt
	names     []string
	done      bool
	closeStmt bool
}

func (r *rows) Columns() []string { return r.names }
func (r *rows) Close() error {
	r.s.c.mu.Lock()
	if !r.done {
		C.sqlite3_reset(r.s.s)
		C.sqlite3_clear_bindings(r.s.s)
		r.done = true
	}
	r.s.c.mu.Unlock()
	if r.closeStmt {
		return r.s.Close()
	}
	return nil
}
func (r *rows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.s.c.mu.Lock()
	defer r.s.c.mu.Unlock()
	rc := C.sqlite3_step(r.s.s)
	if rc == C.SQLITE_DONE {
		r.done = true
		C.sqlite3_reset(r.s.s)
		C.sqlite3_clear_bindings(r.s.s)
		return io.EOF
	}
	if rc != C.SQLITE_ROW {
		return r.s.c.err()
	}
	for i := range dest {
		switch C.sqlite3_column_type(r.s.s, C.int(i)) {
		case C.SQLITE_INTEGER:
			dest[i] = int64(C.sqlite3_column_int64(r.s.s, C.int(i)))
		case C.SQLITE_FLOAT:
			dest[i] = float64(C.sqlite3_column_double(r.s.s, C.int(i)))
		case C.SQLITE_TEXT:
			p := C.sqlite3_column_text(r.s.s, C.int(i))
			n := C.sqlite3_column_bytes(r.s.s, C.int(i))
			dest[i] = C.GoStringN((*C.char)(unsafe.Pointer(p)), n)
		case C.SQLITE_BLOB:
			p := C.sqlite3_column_blob(r.s.s, C.int(i))
			n := C.sqlite3_column_bytes(r.s.s, C.int(i))
			if p == nil || n == 0 {
				dest[i] = []byte{}
			} else {
				dest[i] = C.GoBytes(p, n)
			}
		default:
			dest[i] = nil
		}
	}
	return nil
}

type result struct{ id, n int64 }

func (r result) LastInsertId() (int64, error) { return r.id, nil }
func (r result) RowsAffected() (int64, error) { return r.n, nil }

var _ driver.Driver = (*drv)(nil)
var _ driver.Conn = (*conn)(nil)
var _ driver.ConnPrepareContext = (*conn)(nil)
var _ driver.ExecerContext = (*conn)(nil)
var _ driver.QueryerContext = (*conn)(nil)
var _ driver.ConnBeginTx = (*conn)(nil)
var _ driver.Pinger = (*conn)(nil)

// keep imports stable across Go toolchains
var _ = runtime.KeepAlive
var _ = strings.Builder{}
