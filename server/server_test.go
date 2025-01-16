package server

import (
	"bytes"
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	sqldialect "entgo.io/ent/dialect/sql"
	"github.com/eidng8/go-utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Server_handles_error(t *testing.T) {
	lsnr, err := net.Listen("tcp", ":0")
	require.Nil(t, err)
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
	svr, _ := setup(t)
	gc, _ := gin.CreateTestContext(httptest.NewRecorder())
	gc.Request = httptest.NewRequest(http.MethodGet, "http://localhost/t", nil)
	svr.LogRequest(gc)
	require.True(t, gc.IsAborted())
	require.Equal(t, http.StatusBadRequest, gc.Writer.Status())
}

func Test_DefaultServer_inserts_null_body(t *testing.T) {
	require.Nil(t, os.Setenv("LOG_DEBUG", "true"))
	times := 10
	s, conn := setup(t)
	for i := 0; i < times; i++ {
		go testGet(t, s)
	}
	time.Sleep(1100 * time.Millisecond)
	requireDbCountWithoutBody(t, conn.DB(), times)
}

func Test_DefaultServer_inserts_body(t *testing.T) {
	require.Nil(t, os.Unsetenv("LOG_DEBUG"))
	times := 10
	s, conn := setup(t)
	for i := 0; i < times; i++ {
		go testPost(t, s)
	}
	time.Sleep(1100 * time.Millisecond)
	requireDbCountWithBody(t, conn.DB(), times)
}

func setup(tb testing.TB) (*Server, *sqldialect.Driver) {
	tb.Helper()
	setupDb(tb)
	require.Nil(tb, os.Setenv("LISTEN", "127.0.0.1:0"))
	svr, conn, sigChan, stopChan, cleanup := DefaultServer()
	svr.Config(
		func(s *Server) {
			s.Engine.GET(
				"/t", func(c *gin.Context) { c.Status(http.StatusOK) },
			)
			s.Engine.POST(
				"/t", func(c *gin.Context) { c.Status(http.StatusOK) },
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
	tb.Helper()
	var count int
	//goland:noinspection SqlNoDataSourceInspection,SqlResolve
	err := db.QueryRow(
		`SELECT COUNT(*) FROM requests WHERE request=? AND headers=? AND body IS NULL;`,
		"GET http://localhost/t", "Content-Type: application/json\r\n\r\n",
	).Scan(&count)
	require.Nil(tb, err)
	require.Equal(tb, expected, count)
}

func requireDbCountWithBody(tb testing.TB, db *sql.DB, expected int) {
	tb.Helper()
	var count int
	//goland:noinspection SqlNoDataSourceInspection,SqlResolve
	err := db.QueryRow(
		`SELECT COUNT(*) FROM requests WHERE request=? AND headers=? AND body=?;`,
		"POST http://localhost/t?a=b%21c",
		"Host: localhost\r\nContent-Type: application/json\r\n\r\n",
		sql.Null[[]byte]{V: []byte(`{"test":"value"}`), Valid: true},
	).Scan(&count)
	require.Nil(tb, err)
	require.Equal(tb, expected, count)
}
