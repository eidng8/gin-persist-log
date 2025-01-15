package server

import (
	"context"
	"fmt"
	"io"
	"strings"

	sqldialect "entgo.io/ent/dialect/sql"
	"github.com/eidng8/go-utils"
)

const numColumns = 3

type RequestRecord struct {
	Request string
	Body    []byte
}

func CreateDefaultTable(conn *sqldialect.Driver) error {
	//goland:noinspection SqlNoDataSourceInspection
	_, err := conn.ExecContext(
		context.Background(),
		`CREATE TABLE IF NOT EXISTS requests (
			id BINARY(16) NOT NULL PRIMARY KEY,
			request VARCHAR(8000) NOT NULL,
			body VARBINARY(65535),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);`,
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
		args[idx] = id.String()
		args[idx+1] = rec.Request
		if nil == rec.Body {
			args[idx+2] = sqldialect.NullString{}
		} else {
			args[idx+2] = rec.Body
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
		sb.WriteString(`INSERT INTO requests (id, request, body) VALUES`)
		sb.Grow(pl * count)
		sb.WriteString(strings.Repeat(ps, count)[1:])
		sb.WriteString(";")
		return sb.String(), args
	}
}
