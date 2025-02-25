package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	sqldialect "entgo.io/ent/dialect/sql"
	"github.com/eidng8/go-db"
	"github.com/eidng8/go-utils"
	"github.com/gin-gonic/gin"
)

var (
	dumpRequest = httputil.DumpRequest
)

// Server is a struct that contains necessary instances.
type Server struct {
	Engine *gin.Engine
	Server *http.Server
	Writer db.CachedWriter
	Logger utils.TaggedLogger
}

// DefaultServer creates a new server with default configurations. It returns:
// 1) the created Server struct; 2) a cleanup function that must be called in
// the main loop; 3) the channel for graceful shutdown signals; and 4) the
// channel to stop the CachedWriter goroutine.
func DefaultServer() (
	*Server, *sqldialect.Driver, chan os.Signal, chan struct{}, func(),
) {
	var logger utils.SimpleTaggedLog
	if d, e := utils.GetEnvBool("LOG_DEBUG", false); d && nil == e {
		logger = utils.NewDebugLogger()
	} else {
		logger = utils.NewLogger()
	}
	logger.Infof("Connecting to database...")
	conn := sqldialect.OpenDB(db.ConnectX())
	err := CreateDefaultTable(conn)
	if err != nil {
		logger.Errorf("Error occured while creating default table: %v", err)
	}
	// Prepare log files
	path := utils.GetEnvWithDefault("DB_FAILED_FILE", "failed_db.log")
	dblog, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	utils.PanicIfError(err)
	path = utils.GetEnvWithDefault("REQ_FAILED_FILE", "failed_req.log")
	reqlog, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	utils.PanicIfError(err)
	// Prepare graceful shutdown signals
	stopChan := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	// Start the background writer
	builder := SqlBuilder(logger, reqlog)
	writer := NewCachedWriter(conn.DB(), builder, logger, dblog)
	writer.Start(stopChan)
	// Create the server
	host := utils.GetEnvWithDefault("LISTEN", ":80")
	svr := http.Server{Addr: host}
	s := NewServer(&svr, writer, logger)
	cleanup := func() {
		defer func() { utils.PanicIfError(conn.Close()) }()
		defer func() { utils.PanicIfError(reqlog.Close()) }()
		defer func() { utils.PanicIfError(dblog.Close()) }()
	}
	return s, conn, sigChan, stopChan, cleanup
}

func NewServer(
	svr *http.Server, writer db.CachedWriter, logger utils.TaggedLogger,
) *Server {
	s := &Server{
		Server: svr, Writer: writer, Logger: logger,
	}
	s.Engine = gin.New()
	s.Engine.Use(s.LogRequest, gin.Logger(), gin.Recovery())
	svr.Handler = s.Engine
	return s
}

func NewCachedWriter(
	sdb *sql.DB, builder func(params []any) (string, []any),
	logger utils.TaggedLogger, log io.Writer,
) *db.MemCachedWriter {
	writer := db.NewMemCachedWriter(sdb, builder, logger)
	retries := utils.ReturnOrPanic(utils.GetEnvUint8("MAX_RETRIES", 3))
	writer.SetRetries(int(retries))
	dur := utils.ReturnOrPanic(utils.GetEnvUint8("INTERVAL", 1))
	writer.SetInterval(time.Duration(dur) * time.Second)
	writer.SetFailedLog(log)
	return writer
}

func (s *Server) Config(fn func(*Server)) { fn(s) }

func (s *Server) LogRequest(gc *gin.Context) {
	var err error
	var body []byte
	rlw := &responseLogWriter{
		body:           bytes.NewBuffer(make([]byte, 0, 65536)),
		ResponseWriter: gc.Writer,
	}
	gc.Writer = rlw
	url := utils.RequestFullUrl(gc.Request)
	method := gc.Request.Method
	var sb strings.Builder
	sb.Grow(len(url) + 10)
	sb.WriteString(method)
	sb.WriteString(" ")
	sb.WriteString(url)
	line := sb.String()
	headers, err := dumpRequest(gc.Request, false)
	if err != nil {
		s.Logger.Errorf("Failed to read request headers: %v", err)
		gc.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if nil != gc.Request.Body {
		body, err = io.ReadAll(gc.Request.Body)
		if err != nil {
			s.Logger.Errorf("Failed to read request body: %v", err)
			gc.AbortWithStatus(http.StatusBadRequest)
			return
		}
		gc.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	}
	s.Writer.Push(TxRecord{line, headers, body, time.Now()})
	gc.Next()
	var buf bytes.Buffer
	buf.Grow(4096)
	_, err = fmt.Fprintf(&buf, "HTTP/1.1 %d %s\r\n", gc.Writer.Status(),
		http.StatusText(gc.Writer.Status()))
	if err != nil {
		s.Logger.Errorf("Failed to dump response status: %v", err)
		return
	}
	err = gc.Writer.Header().Clone().Write(&buf)
	if err != nil {
		s.Logger.Errorf("Failed to dump response headers: %v", err)
		return
	}
	s.Writer.Push(TxRecord{line, buf.Bytes(), rlw.body.Bytes(), time.Now()})
}

func (s *Server) Serve() {
	s.Logger.Infof("Serving on %s", s.Server.Addr)
	if strings.HasPrefix(s.Server.Addr, "unix:") {
		sock, err := net.Listen("unix", s.Server.Addr[5:])
		if nil != err {
			s.Logger.Panicf("Listen error: %v", err)
		}
		err = s.Server.Serve(sock)
		if nil != err && !errors.Is(err, http.ErrServerClosed) {
			s.Logger.Panicf("Serve error: %v", err)
		}
	} else {
		err := s.Server.ListenAndServe()
		if nil != err && !errors.Is(err, http.ErrServerClosed) {
			s.Logger.Panicf("ListenAndServe error: %v", err)
		}
	}
}

func (s *Server) Shutdown() (context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	return cancel, s.Server.Shutdown(ctx)
}
