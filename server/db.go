package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	sqldialect "entgo.io/ent/dialect/sql"
	"github.com/eidng8/go-utils"
)

const numColumns = 4

type RequestRecord struct {
	Request string
	Headers []byte
	Body    []byte
}

func CreateDefaultTable(conn *sqldialect.Driver) error {
	// Only MySQL, SQLite, and Oracle support BLOB
	//goland:noinspection SqlNoDataSourceInspection
	_, err := conn.ExecContext(
		context.Background(),
		`CREATE TABLE IF NOT EXISTS requests (
			id BINARY(16) NOT NULL PRIMARY KEY,
			request TEXT NOT NULL,
			headers TEXT NOT NULL,
			body BLOB,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
	)
	return err
}

func BuildValues(data interface{}) (
	count int, args []any, failed []RequestRecord, err error,
) {
	records := data.([]interface{})
	c := len(records)
	args = make([]any, c*numColumns)
	for _, d := range records {
		rec := d.(RequestRecord)
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
		args[idx+1] = rec.Request
		args[idx+2] = string(rec.Headers)
		if nil == rec.Body || 0 == len(rec.Body) {
			args[idx+3] = sql.Null[[]byte]{}
		} else {
			args[idx+3] = sql.Null[[]byte]{V: rec.Body, Valid: true}
		}
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
		sb.WriteString(`INSERT INTO requests (id, request, headers, body) VALUES`)
		sb.Grow(pl * count)
		sb.WriteString(strings.Repeat(ps, count)[1:])
		sb.WriteString(";")
		return sb.String(), args
	}
}
