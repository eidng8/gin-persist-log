package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	sqldialect "entgo.io/ent/dialect/sql"
	"github.com/cespare/xxhash/v2"
	"github.com/eidng8/go-utils"
)

const numColumns = 5

type TxRecord struct {
	// can be empty for responses
	Request string
	Headers []byte
	Body    []byte
	At      time.Time
}

func CreateDefaultTable(conn *sqldialect.Driver) error {
	// var tableStmt string
	// ctx := context.Background()
	// switch conn.Dialect() {
	// case "mysql":
	// 	// language=mysql
	// 	//goland:noinspection SqlNoDataSourceInspection
	// 	tableStmt = `
	// 	CREATE TABLE IF NOT EXISTS tx_log (
	// 		id BINARY(16) NOT NULL PRIMARY KEY,
	// 		req_hash BINARY(16) NOT NULL KEY,
	// 		headers TEXT NOT NULL,
	// 		body BLOB,
	// 		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	// 	)`
	// 	if _, err := conn.ExecContext(ctx, tableStmt); err != nil {
	// 		return err
	// 	}
	//
	// case "sqlite3":
	// 	// language=sqlite
	// 	//goland:noinspection SqlNoDataSourceInspection
	// 	tableStmt = `
	// 	CREATE TABLE IF NOT EXISTS tx_log (
	// 		id BLOB PRIMARY KEY,
	// 		req_hash BLOB NOT NULL,
	// 		headers TEXT NOT NULL,
	// 		body BLOB,
	// 		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	// 	);
	// 	CREATE INDEX ix_tx_log_hash ON tx_log (req_hash);`
	//
	// case "postgres":
	// 	tableStmt = `
	// 	CREATE TABLE IF NOT EXISTS tx_log (
	// 		id BYTEA PRIMARY KEY,
	// 		req_hash BYTEA NOT NULL,
	// 		headers TEXT NOT NULL,
	// 		body BYTEA,
	// 		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	// 	)`
	// 	if _, err := conn.ExecContext(ctx, tableStmt); err != nil {
	// 		return err
	// 	}
	// 	if _, err := conn.ExecContext(ctx,
	// 		`CREATE INDEX IF NOT EXISTS ix_tx_log_hash ON tx_log (req_hash)`); err != nil {
	// 		return err
	// 	}
	//
	// case "sqlserver":
	// 	tableStmt = `
	// 	IF NOT EXISTS (SELECT * FROM sys.tables WHERE name = 'tx_log')
	// 	BEGIN
	// 		CREATE TABLE tx_log (
	// 			id BINARY(16) NOT NULL PRIMARY KEY,
	// 			req_hash BINARY(16) NOT NULL,
	// 			headers NVARCHAR(MAX) NOT NULL,
	// 			body VARBINARY(MAX),
	// 			created_at DATETIME NOT NULL DEFAULT GETDATE()
	// 		)
	// 	END`
	// 	if _, err := conn.ExecContext(ctx, tableStmt); err != nil {
	// 		return err
	// 	}
	// 	indexStmt := `
	// 	IF NOT EXISTS (
	// 		SELECT * FROM sys.indexes
	// 		WHERE name = 'ix_tx_log_hash' AND object_id = OBJECT_ID('tx_log')
	// 	)
	// 	BEGIN
	// 		CREATE INDEX ix_tx_log_hash ON tx_log (req_hash)
	// 	END`
	// 	if _, err := conn.ExecContext(ctx, indexStmt); err != nil {
	// 		return err
	// 	}
	//
	// case "oracle":
	// 	// Oracle requires a PL/SQL block to conditionally create the table.
	// 	tableStmt = `
	// 	DECLARE
	// 		cnt NUMBER;
	// 	BEGIN
	// 		SELECT COUNT(*) INTO cnt FROM user_tables WHERE table_name = UPPER('tx_log');
	// 		IF cnt = 0 THEN
	// 			EXECUTE IMMEDIATE 'CREATE TABLE tx_log (
	// 				id RAW(16) PRIMARY KEY,
	// 				req_hash RAW(16) NOT NULL,
	// 				headers CLOB NOT NULL,
	// 				body BLOB,
	// 				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
	// 			)';
	// 		END IF;
	// 	END;`
	// 	if _, err := conn.ExecContext(ctx, tableStmt); err != nil {
	// 		return err
	// 	}
	// 	indexStmt := `
	// 	DECLARE
	// 		cnt NUMBER;
	// 	BEGIN
	// 		SELECT COUNT(*) INTO cnt FROM user_indexes WHERE index_name = UPPER('ix_tx_log_hash') AND table_name = UPPER('tx_log');
	// 		IF cnt = 0 THEN
	// 			EXECUTE IMMEDIATE 'CREATE INDEX ix_tx_log_hash ON tx_log (req_hash)';
	// 		END IF;
	// 	END;`
	// 	if _, err := conn.ExecContext(ctx, indexStmt); err != nil {
	// 		return err
	// 	}
	//
	// default:
	// 	return fmt.Errorf("unsupported dialect: %s", dialect)
	// }

	// Only MySQL, SQLite, and Oracle support BLOB
	//goland:noinspection SqlNoDataSourceInspection
	_, err := conn.ExecContext(
		context.Background(),
		`CREATE TABLE IF NOT EXISTS tx_log (
			id BINARY(16) NOT NULL PRIMARY KEY,
			req_hash BINARY(16) NOT NULL,
			headers TEXT NOT NULL,
			body BLOB,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE INDEX ix_tx_log_hash ON tx_log (req_hash);`,
	)
	return err
}

func BuildValues(data interface{}) (
	count int, args []any, failed []TxRecord, err error,
) {
	records := data.([]interface{})
	c := len(records)
	args = make([]any, c*numColumns)
	hasher := xxhash.New()
	for _, d := range records {
		rec := d.(TxRecord)
		id, e := utils.NewUuid()
		if nil != e {
			err = fmt.Errorf("error generating UUID: %w", e)
			failed = append(failed, rec)
			continue
		}
		idx := count * numColumns
		args[idx], e = id.MarshalBinary()
		if nil != e {
			err = fmt.Errorf("error marshaling UUID: %w", e)
			failed = append(failed, rec)
			continue
		}
		if "" == rec.Request {
			return 0, nil, nil, errors.New("empty_request")
		}
		hasher.Reset()
		_, err = hasher.WriteString(rec.Request)
		if nil != err {
			return 0, nil, nil, err
		}
		// work around error uint64 values with high bit set are not supported
		args[idx+1] = fmt.Sprintf("%016x", hasher.Sum64())
		args[idx+2] = string(rec.Headers)
		if nil == rec.Body || 0 == len(rec.Body) {
			args[idx+3] = sql.Null[[]byte]{}
		} else {
			args[idx+3] = sql.Null[[]byte]{V: rec.Body, Valid: true}
		}
		args[idx+4] = rec.At.Format("2006-01-02 15:04:05.000000")
		count++
	}
	return
}

func SqlBuilder(log utils.TaggedLogger, failed io.Writer) func(data []any) (
	string, []any,
) {
	return func(data []any) (string, []any) {
		count, args, fails, err := BuildValues(data)
		if nil != err {
			log.Errorf("error building values: %v\n", err)
			for _, f := range fails {
				_, err = fmt.Fprintf(failed, "%#v;\n", f)
				if nil != err {
					log.Errorf("can't log fails: %s\n", err.Error())
				}
			}
			return "", nil
		}
		var sb strings.Builder
		pl := numColumns*2 + 2
		sb.Grow(pl)
		sb.WriteString(",(")
		sb.WriteString(strings.Repeat(",?", numColumns)[1:])
		sb.WriteString(")")
		ps := sb.String()
		sb.Reset()
		sb.WriteString(`INSERT INTO tx_log (id, req_hash, headers, body, created_at) VALUES`)
		sb.Grow(pl * count)
		sb.WriteString(strings.Repeat(ps, count)[1:])
		sb.WriteString(";")
		return sb.String(), args
	}
}

// func ConnectDB() (*sql.DB, error) {
// 	drv := os.Getenv("DB_DRIVER")
// 	if "" == drv {
// 		return drv, nil, errors.New("DB_DRIVER environment variable is not set")
// 	}
// 	dsn := os.Getenv("DB_DSN")
// 	if "" != dsn {
// 		conn, err := sql.Open(drv, dsn)
// 		if err != nil {
// 			return drv, nil, err
// 		}
// 		return drv, conn, nil
// 	}
// 	sss, ccc := sql.Open("sqlite3", "file:memdb1?mode=memory&cache=shared")
// 	return sss
// }
