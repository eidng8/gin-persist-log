package server

import (
	"bytes"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	sqldialect "entgo.io/ent/dialect/sql"
	"github.com/cespare/xxhash/v2"
	"github.com/eidng8/go-utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_BuildValues_with_response(t *testing.T) {
	var rec interface{} = TxRecord{
		Request: "abc",
		Headers: []byte("test header"),
		Body:    []byte("test body"),
	}
	data := []interface{}{rec}
	count, args, _, err := BuildValues(data)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Len(t, args, numColumns)
	require.IsType(t, []byte{}, args[0])
	require.Len(t, args[0], 16)
	hasher := xxhash.New()
	_, err = hasher.WriteString("abc")
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%016x", hasher.Sum64()), args[1])
	require.Equal(t, "test header", args[2])
	require.Equal(t,
		sql.Null[[]byte]{V: []byte("test body"), Valid: true}, args[3])
}

func Test_BuildValues_empty_response_returns_error(t *testing.T) {
	var rec interface{} = TxRecord{
		Request: "",
		Headers: []byte("test header"),
		Body:    []byte("test body"),
	}
	data := []interface{}{rec}
	_, _, _, err := BuildValues(data)
	require.NotNil(t, err)
}

func Test_Server_handles_error(t *testing.T) {
	lsnr, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	svr := Server{
		Logger: utils.NewLogger(),
		Server: &http.Server{Addr: lsnr.Addr().String()},
	}
	require.Panics(t, svr.Serve)
}

func Test_LogRequest_handles_dump_error(t *testing.T) {
	of := dumpRequest
	defer func() { dumpRequest = of }()
	dumpRequest = func(r *http.Request, body bool) ([]byte, error) {
		return nil, assert.AnError
	}
	listens := []string{"127.0.0.1:0", "unix:/tmp/test.sock"}
	for _, listen := range listens {
		t.Run(listen, func(t *testing.T) {
			if "windows" == runtime.GOOS && strings.HasPrefix(listen, "unix:") {
				t.Skip("skipping on windows")
			}
			require.Nil(t, os.Setenv("LISTEN", listen))
			svr, _ := setup(t)
			gc, _ := gin.CreateTestContext(httptest.NewRecorder())
			gc.Request = httptest.NewRequest(http.MethodGet,
				"http://localhost/t", nil)
			svr.LogRequest(gc)
			require.True(t, gc.IsAborted())
			require.Equal(t, http.StatusBadRequest, gc.Writer.Status())
		})
	}
}

func Test_DefaultServer_inserts_null_body(t *testing.T) {
	require.Nil(t, os.Setenv("LOG_DEBUG", "true"))
	times := 10
	listens := []string{"127.0.0.1:0", "unix:/tmp/test.sock"}
	for _, listen := range listens {
		t.Run(listen, func(t *testing.T) {
			if "windows" == runtime.GOOS && strings.HasPrefix(listen, "unix:") {
				t.Skip("skipping on windows")
			}
			require.Nil(t, os.Setenv("LISTEN", listen))
			s, conn := setup(t)
			for i := 0; i < times; i++ {
				go testGet(t, s)
			}
			time.Sleep(1100 * time.Millisecond)
			requireDbCountWithoutBody(t, conn.DB(), times)
		})
	}
}

func Test_DefaultServer_inserts_body(t *testing.T) {
	require.Nil(t, os.Unsetenv("LOG_DEBUG"))
	times := 10
	listens := []string{"127.0.0.1:0", "unix:/tmp/test.sock"}
	for _, listen := range listens {
		t.Run(listen, func(t *testing.T) {
			if "windows" == runtime.GOOS && strings.HasPrefix(listen, "unix:") {
				t.Skip("skipping on windows")
			}
			require.Nil(t, os.Setenv("LISTEN", listen))
			s, conn := setup(t)
			for i := 0; i < times; i++ {
				go testPost(t, s)
			}
			time.Sleep(1100 * time.Millisecond)
			db := conn.DB()
			var count int
			hasher := xxhash.New()
			_, err := hasher.WriteString("POST http://localhost/t?a=b%21c")
			require.Nil(t, err)
			hs := fmt.Sprintf("%016x", hasher.Sum64())
			//goland:noinspection SqlNoDataSourceInspection,SqlResolve
			err = db.QueryRow(
				`SELECT COUNT(*) FROM tx_log WHERE req_hash=? AND headers=? AND body=?;`,
				hs,
				"POST /t?a=b%21c HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\n\r\n",
				sql.Null[[]byte]{V: []byte(`{"test":"value"}`), Valid: true},
			).Scan(&count)
			require.Nil(t, err)
			require.Equal(t, times, count)
			//goland:noinspection SqlNoDataSourceInspection,SqlResolve
			err = db.QueryRow(
				`SELECT COUNT(*) FROM tx_log WHERE req_hash=? AND headers=? AND body=?;`,
				hs,
				"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n",
				sql.Null[[]byte]{V: []byte(`"post ok"`), Valid: true},
			).Scan(&count)
			require.Nil(t, err)
			require.Equal(t, times, count)
		})
	}
}

func setup(tb testing.TB) (*Server, *sqldialect.Driver) {
	tb.Helper()
	setupDb(tb)
	svr, conn, sigChan, stopChan, cleanup := DefaultServer()
	svr.Config(
		func(s *Server) {
			s.Engine.GET(
				"/t", func(c *gin.Context) {
					c.Data(http.StatusOK, "application/json",
						[]byte(`"get ok"`))
				},
			)
			s.Engine.POST(
				"/t", func(c *gin.Context) {
					c.Data(http.StatusOK, "application/json",
						[]byte(`"post ok"`))
				},
			)
		},
	)
	tb.Cleanup(
		func() {
			sigChan <- syscall.SIGTERM
			close(stopChan)
			cancel, _ := svr.Shutdown()
			defer cancel()
			defer cleanup()
		},
	)
	go svr.Serve()
	return svr, conn
}

func testGet(tb testing.TB, s *Server) *httptest.ResponseRecorder {
	tb.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/t", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Engine.ServeHTTP(w, req)
	require.Equal(tb, http.StatusOK, w.Code)
	return w
}

func testPost(tb testing.TB, s *Server) *httptest.ResponseRecorder {
	tb.Helper()
	body := []byte(`{"test":"value"}`)
	req := httptest.NewRequest(
		http.MethodPost, "/t?a=b%21c", bytes.NewReader(body),
	)
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Engine.ServeHTTP(w, req)
	require.Equal(tb, http.StatusOK, w.Code)
	return w
}

func requireDbCountWithoutBody(tb testing.TB, db *sql.DB, expected int) {
	var count int
	hasher := xxhash.New()
	_, err := hasher.WriteString("GET http://localhost/t")
	require.Nil(tb, err)
	hs := fmt.Sprintf("%016x", hasher.Sum64())
	//goland:noinspection SqlNoDataSourceInspection,SqlResolve
	err = db.QueryRow(
		`SELECT COUNT(*) FROM tx_log WHERE req_hash=? AND headers=? AND body IS NULL;`,
		hs,
		"GET http://localhost/t HTTP/1.1\r\nContent-Type: application/json\r\n\r\n",
	).Scan(&count)
	require.Nil(tb, err)
	require.Equal(tb, expected, count)
	//goland:noinspection SqlNoDataSourceInspection,SqlResolve
	err = db.QueryRow(
		`SELECT COUNT(*) FROM tx_log WHERE req_hash=? AND headers=? AND body=?;`,
		hs,
		"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n",
		sql.Null[[]byte]{V: []byte(`"get ok"`), Valid: true},
	).Scan(&count)
	require.Nil(tb, err)
	require.Equal(tb, expected, count)
}

func requireDbCountWithBody(tb testing.TB, db *sql.DB, expected int) {
	var count int
	hasher := xxhash.New()
	_, err := hasher.WriteString("POST http://localhost/t?a=b%21c")
	require.Nil(tb, err)
	hs := fmt.Sprintf("%016x", hasher.Sum64())
	//goland:noinspection SqlNoDataSourceInspection,SqlResolve
	err = db.QueryRow(
		`SELECT COUNT(*) FROM tx_log WHERE req_hash=? AND headers=? AND body=?;`,
		hs,
		"POST /t?a=b%21c HTTP/1.1\r\nContent-Type: application/json\r\n",
		sql.Null[[]byte]{V: []byte(`"post ok"`), Valid: true},
	).Scan(&count)
	require.Nil(tb, err)
	require.Equal(tb, expected, count)
	//goland:noinspection SqlNoDataSourceInspection,SqlResolve
	err = db.QueryRow(
		`SELECT COUNT(*) FROM tx_log WHERE req_hash=? AND headers=? AND body=?;`,
		hs,
		"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n",
		sql.Null[[]byte]{V: []byte(`"post ok"`), Valid: true},
	).Scan(&count)
	require.Nil(tb, err)
	require.Equal(tb, expected, count)
}
