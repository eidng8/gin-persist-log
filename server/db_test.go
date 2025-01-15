package server

import (
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/stretchr/testify/require"
)

func setupDb(tb testing.TB) {
	require.Nil(tb, os.Setenv("DB_DRIVER", "sqlite3"))
	require.Nil(tb, os.Setenv("DB_DSN", ":memory:?_journal=WAL&_timeout=5000"))
}