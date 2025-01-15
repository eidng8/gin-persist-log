package server

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	sqldialect "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func Test_DefaultServer(t *testing.T) {
	times := 10
	s, conn := setup(t)
	for i := 0; i < times; i++ {
		go testPost(t, s)
	}
	time.Sleep(1100 * time.Millisecond)
	requireDbCount(t, conn.DB(), times)
}

func setup(tb testing.TB) (*Server, *sqldialect.Driver) {
	tb.Helper()
	setupDb(tb)
	svr, conn, sigChan, stopChan, cleanup := DefaultServer()
	svr.Config(
		func(s *Server) {
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
	return svr, conn
}

func testPost(tb testing.TB, s *Server) *httptest.ResponseRecorder {
	tb.Helper()
	body := []byte(`{"test":"value"}`)
	req := httptest.NewRequest(http.MethodPost, "/t", bytes.NewReader(body))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Engine.ServeHTTP(w, req)
	require.Equal(tb, http.StatusOK, w.Code)
	return w
}

func requireDbCount(tb testing.TB, db *sql.DB, expected int) {
	tb.Helper()
	var count int
	//goland:noinspection SqlNoDataSourceInspection,SqlResolve
	err := db.QueryRow("SELECT COUNT(*) FROM requests").Scan(&count)
	require.Nil(tb, err)
	require.Equal(tb, expected, count)
}
