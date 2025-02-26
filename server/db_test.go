package server

import (
	"bytes"
	"database/sql"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/eidng8/go-utils"
	ut "github.com/eidng8/go-utils/testing"
	"github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eidng8/gin-persist-log/internal"
)

type mockHasher struct {
	internal.XxHasher
}

func (m *mockHasher) WriteString(_ string) (n int, err error) {
	return 0, assert.AnError
}

var _ internal.Hasher = &mockHasher{}

func Test_ConnectDB_returns_error_if_no_driver(t *testing.T) {
	_, err := ConnectDB(&DbConfig{Dsn: ":memory:"})
	require.NotNil(t, err)
	require.Equal(t, "invalid DB driver", err.Error())
}

func Test_ConnectDB_returns_error_if_no_dsn(t *testing.T) {
	_, err := ConnectDB(&DbConfig{Driver: "sqlite3"})
	require.NotNil(t, err)
	require.Equal(t, "invalid DSN", err.Error())
}

func Test_ConnectDB_returns_error_if_invalid_config(t *testing.T) {
	_, err := ConnectDB(&DbConfig{Driver: "abc", Dsn: "def"})
	require.NotNil(t, err)
}

//goland:noinspection SqlNoDataSourceInspection,SqlResolve
func Test_CreateDefaultTable_supports_mysql(t *testing.T) {
	if _, err := os.Stat("/.dockerenv"); nil != err && "linux" != runtime.GOOS {
		t.Skip("Only run in linux docker container")
	}
	cfg := DbConfig{
		Driver: "mysql",
		Dsn: (&mysql.Config{
			Addr:                 "mysql:3306",
			DBName:               "test",
			Net:                  "tcp",
			Passwd:               "123456",
			User:                 "root",
			AllowNativePasswords: true,
			ParseTime:            true,
			MultiStatements:      true,
		}).FormatDSN(),
	}
	conn, err := ConnectDB(&cfg)
	require.Nil(t, err)
	require.Nil(t, CreateDefaultTable(&cfg, conn))
	_, err = conn.Query(`SELECT id,req_hash,headers,body,created_at FROM tx_log;`)
	require.Nil(t, err)
}

//goland:noinspection SqlNoDataSourceInspection,SqlResolve
func Test_CreateDefaultTable_supports_sqlite(t *testing.T) {
	cfg := DbConfig{
		Driver:  "sqlite3",
		Dsn:     ":memory:?_journal=WAL&_timeout=5000",
		Dialect: "sqlite3",
	}
	conn, err := ConnectDB(&cfg)
	require.Nil(t, err)
	require.Nil(t, CreateDefaultTable(&cfg, conn))
	_, err = conn.Query(`SELECT id,req_hash,headers,body,created_at FROM tx_log;`)
	require.Nil(t, err)
}

func Test_CreateDefaultTable_returns_error_if_not_support(t *testing.T) {
	cfg := DbConfig{
		Driver:  "sqlite3",
		Dsn:     ":memory:?_journal=WAL&_timeout=5000",
		Dialect: "sqlite",
	}
	conn, err := ConnectDB(&cfg)
	require.Nil(t, err)
	err = CreateDefaultTable(&cfg, conn)
	require.NotNil(t, err)
	require.Equal(t, "unsupported SQL dialect", err.Error())
}

func Test_BuildValues_returns_error_if_invalid_records(t *testing.T) {
	_, _, _, err := BuildValues([]byte{1, 2, 3})
	require.NotNil(t, err)
	require.Equal(t, "invalid_records", err.Error())
}

func Test_BuildValues_returns_error_if_invalid_record(t *testing.T) {
	_, _, _, err := BuildValues([]interface{}{1, 2, 3})
	require.NotNil(t, err)
	require.Equal(t, "invalid record: 3", err.Error())
}

func Test_BuildValues_returns_error_if_uuid_new_error(t *testing.T) {
	defer func() { uuid = &utils.Uuid{} }()
	mock := ut.NewUuidMock(ut.MockUuidConfig{NewReturnsError: true})
	uuid = &mock
	_, _, _, err := BuildValues([]interface{}{
		TxRecord{
			Request: "req",
			Headers: []byte("test header"),
			Body:    []byte("test body"),
			At:      time.Now(),
		},
	})
	require.NotNil(t, err)
	require.Equal(t,
		"error generating UUID: assert.AnError general error for testing",
		err.Error())
}

func Test_BuildValues_returns_error_if_uuid_marshal_error(t *testing.T) {
	defer func() { uuid = &utils.Uuid{} }()
	mock := ut.NewUuidMock(ut.MockUuidConfig{MarshalBinaryReturnsError: true})
	uuid = &mock
	_, _, _, err := BuildValues([]interface{}{
		TxRecord{
			Request: "req",
			Headers: []byte("test header"),
			Body:    []byte("test body"),
			At:      time.Now(),
		},
	})
	require.NotNil(t, err)
	require.Equal(t,
		"error marshaling UUID: assert.AnError general error for testing",
		err.Error())
}

func Test_BuildValues_returns_error_if_hasher_write_error(t *testing.T) {
	defer func() { hasher = &internal.XxHasher{} }()
	hasher = &mockHasher{}
	_, _, _, err := BuildValues([]interface{}{
		TxRecord{
			Request: "req",
			Headers: []byte("test header"),
			Body:    []byte("test body"),
			At:      time.Now(),
		},
	})
	require.ErrorIs(t, assert.AnError, err)
}

func Test_SqlBuilder_returns_nil_if_BuildValues_error(t *testing.T) {
	var buf bytes.Buffer
	logger := utils.NewStringTaggedLogger()
	fn := SqlBuilder(logger, &buf)
	s, a := fn([]interface{}{1, 2, 3})
	require.Equal(t, "[ERROR] error building values: invalid record: 3\n",
		logger.String())
	require.Equal(t, 3, strings.Count(buf.String(), "server.TxRecord"))
	require.Empty(t, s)
	require.Nil(t, a)
}

func Test_SqlBuilder_returns_nil_if_Fprintf_error(t *testing.T) {
	logger := utils.NewStringTaggedLogger()
	fn := SqlBuilder(logger, &mockWriter{})
	s, a := fn([]interface{}{1, 2, 3})
	require.Equal(
		t,
		"[ERROR] error building values: invalid record: 3\n"+
			strings.Repeat("[ERROR] can't log fails: assert.AnError general error for testing\n",
				3),
		logger.String())
	require.Empty(t, s)
	require.Nil(t, a)
}

func setupDb(tb testing.TB) (*DbConfig, *sql.DB) {
	cfg := DbConfig{
		Driver: "sqlite3",
		Dsn:    ":memory:?_journal=WAL&_timeout=5000",
	}
	conn, err := ConnectDB(&cfg)
	require.Nil(tb, err)
	require.Nil(tb, CreateDefaultTable(&cfg, conn))
	return &cfg, conn
	// require.Nil(tb, os.Setenv("DB_DRIVER", "sqlite3"))
	// require.Nil(tb, os.Setenv("DB_DSN", ":memory:?_journal=WAL&_timeout=5000"))
	// require.Nil(tb, os.Setenv("DB_DRIVER", "mysql"))
	// require.Nil(tb, os.Setenv("DB_DSN", "du:pass@tcp(127.0.0.1:32768)/test"))
}

type mockWriter struct{}

func (w *mockWriter) Write(p []byte) (n int, err error) {
	return 0, assert.AnError
}

var _ io.Writer = &mockWriter{}
