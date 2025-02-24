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
		CREATE INDEX IF NOT EXISTS ix_tx_log_hash ON tx_log (req_hash);`,
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
