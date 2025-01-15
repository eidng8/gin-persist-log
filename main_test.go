package main

import (
	"bufio"
	"bytes"
	"log"
	"net/http"
	"testing"

	sqldialect "entgo.io/ent/dialect/sql"
	"github.com/eidng8/go-db"
	"github.com/eidng8/go-utils"
)

func setup(tb testing.TB) (*Server, *sqldialect.Driver) {
	// make string io writer
	buf := bytes.NewBuffer([]byte{})
	// make logger
	logger := utils.WrapLogger(log.New(buf, "test", 0), true)
	var br bytes.Buffer
	wr := bufio.NewWriter(&br)
	conn := setupDb(tb)
	writer := db.NewMemCachedWriter(conn.DB(), sqlBuilder(logger, wr))
	s := NewServer(&http.Server{}, writer, logger)
	return s, conn
}
