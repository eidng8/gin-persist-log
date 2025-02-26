package main

import (
	"os"

	"github.com/eidng8/go-utils"

	"github.com/eidng8/gin-persist-log/server"
)

func main() {
	cfg := server.ConnConfig{
		Driver: os.Getenv("DB_DRIVER"),
		Dsn:    os.Getenv("DB_DSN"),
	}
	conn, err := server.ConnectDB(&cfg)
	utils.PanicIfError(server.CreateDefaultTable(&cfg, conn))
	svr, sigChan, stopChan, cleanup := server.DefaultServer(conn)
	defer cleanup()
	svr.Config(route)
	go svr.Serve()

	// Wait for shutdown signal
	sig := <-sigChan
	svr.Logger.Infof("Received signal: %v. Shutting down...\n", sig)

	// Stop the background writer
	close(stopChan)

	// Shutdown the server
	cancel, err := svr.Shutdown()
	defer cancel()
	if nil != err {
		svr.Logger.Errorf("Server Shutdown error: %v\n", err)
		os.Exit(1)
	}

	svr.Logger.Infof("Server gracefully stopped. Bye.")
}

func route(s *server.Server) {
	// 下游回调事件
	// s.Engine.GET("/cb", handleCallback(s))
	// 上游触发事件
	// s.Engine.GET("/up", handleUpstream(s))
	// 上游唤端事件
	// s.Engine.GET("/wk", handleWake(s))
	// 下游归因事件
	// s.Engine.POST("/dn", handleDownstream(s))
}
