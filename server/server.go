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

	"github.com/eidng8/go-db"
	"github.com/eidng8/go-utils"
	"github.com/gin-gonic/gin"

	"github.com/eidng8/gin-persist-log/internal"
)

var (
	readBody             = io.ReadAll
	serveSock            = serveSocket
	listenSock           = listenSocket
	dumpRequest          = httputil.DumpRequest
	writeResponseLine    = writeResLine
	writeResponseHeaders = writeResHeaders
)

// Server is a struct that contains necessary instances.
type Server struct {
	Engine *gin.Engine
	Server *http.Server
	Writer db.CachedWriter
	Logger utils.TaggedLogger
}

type Config struct {
	// path to file to log failed requests
	RequestLogFile string
	// path to file to log failed db requests
	DbLogFile string
	// permission of log files to be created
	FilePerm os.FileMode
	// signals to listen for graceful shutdown
	TermSignals []os.Signal
	// address to listen on
	ListenAddr string
	// whether to log debug info
	DebugLog bool
}

func DefaultConfigFromEnv() *Config {
	mode, err := utils.GetEnvUint32("LOG_FILE_MODE", 0644)
	utils.PanicIfError(err)
	debug, err := utils.GetEnvBool("LOG_DEBUG", false)
	utils.PanicIfError(err)
	return &Config{
		RequestLogFile: utils.GetEnvWithDefault("REQ_FAILED_FILE",
			"failed_req.log"),
		DbLogFile: utils.GetEnvWithDefault("DB_FAILED_FILE",
			"failed_db.log"),
		FilePerm:    os.FileMode(mode),
		TermSignals: []os.Signal{syscall.SIGINT, syscall.SIGTERM},
		ListenAddr:  utils.GetEnvWithDefault("LISTEN", ":80"),
		DebugLog:    debug,
	}
}

// DefaultServer creates a new server with default configurations. It returns:
// 1) the created Server struct; 2) a cleanup function that must be called in
// the main loop; 3) the channel for graceful shutdown signals; and 4) the
// channel to stop the CachedWriter goroutine.
func DefaultServer(conn *sql.DB, cfg *Config) (
	*Server, chan os.Signal, chan struct{}, func(),
) {
	logger := createLogger(cfg)
	// Prepare log files
	dblog, err := os.OpenFile(cfg.DbLogFile,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, cfg.FilePerm)
	utils.PanicIfError(err)
	reqlog, err := os.OpenFile(cfg.RequestLogFile,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, cfg.FilePerm)
	utils.PanicIfError(err)
	// Prepare graceful shutdown signals
	stopChan := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, cfg.TermSignals...)
	// Start the background writer
	builder := SqlBuilder(logger, reqlog)
	writer := NewCachedWriter(conn, builder, logger, dblog)
	writer.Start(stopChan)
	// Create the server
	svr := http.Server{Addr: cfg.ListenAddr}
	s := NewServer(&svr, writer, logger)
	cleanup := func() {
		defer func() { utils.PanicIfError(conn.Close()) }()
		defer func() { utils.PanicIfError(reqlog.Close()) }()
		defer func() { utils.PanicIfError(dblog.Close()) }()
	}
	return s, sigChan, stopChan, cleanup
}

func NewServer(
	svr *http.Server, writer db.CachedWriter, logger utils.TaggedLogger,
) *Server {
	s := &Server{
		Server: svr, Writer: writer, Logger: logger,
	}
	s.Engine = gin.New()
	s.Engine.Use(s.RequestLogger(), gin.Logger(), gin.Recovery())
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

func (s *Server) RequestLogger() func(gc *gin.Context) {
	return func(gc *gin.Context) {
		var err error
		var body []byte
		rlw := &internal.ResponseLogWriter{
			Body:           bytes.NewBuffer(make([]byte, 0, 65536)),
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
			body, err = readBody(gc.Request.Body)
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
		if err = writeResponseLine(gc, &buf); err != nil {
			s.Logger.Errorf("Failed to dump response status: %v", err)
			gc.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		if err = writeResponseHeaders(gc, &buf); err != nil {
			s.Logger.Errorf("Failed to dump response headers: %v", err)
			gc.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		s.Writer.Push(TxRecord{line, buf.Bytes(), rlw.Body.Bytes(), time.Now()})
	}
}

func (s *Server) Serve() {
	s.Logger.Infof("Serving on %s", s.Server.Addr)
	if strings.HasPrefix(s.Server.Addr, "unix:") {
		sock, err := listenSock(s.Server.Addr[5:])
		if nil != err {
			s.Logger.Panicf("Listen error: %v", err)
		}
		err = serveSock(s, sock)
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

func createLogger(cfg *Config) utils.TaggedLogger {
	if cfg.DebugLog {
		return utils.NewDebugLogger()
	}
	return utils.NewLogger()
}

func writeResLine(gc *gin.Context, writer io.Writer) error {
	_, err := fmt.Fprintf(writer, "HTTP/1.1 %d %s\r\n", gc.Writer.Status(),
		http.StatusText(gc.Writer.Status()))
	return err
}

func writeResHeaders(gc *gin.Context, writer io.Writer) error {
	return gc.Writer.Header().Clone().Write(writer)
}

func listenSocket(addr string) (net.Listener, error) {
	return net.Listen("unix", addr)
}

func serveSocket(s *Server, sock net.Listener) error {
	return s.Server.Serve(sock)
}
