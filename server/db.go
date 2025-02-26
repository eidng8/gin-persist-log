package server

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/eidng8/go-utils"

	"github.com/eidng8/gin-persist-log/internal"
)

const numColumns = 5

var uuid utils.UUID = &utils.Uuid{}
var hasher internal.Hasher = &internal.XxHasher{}

type DbConfig struct {
	Driver, Dsn string
	// Optional, just in case can't determine from `Driver`
	Dialect string
}

type TxRecord struct {
	Request string
	Headers []byte
	Body    []byte
	At      time.Time
}

func CreateDefaultTable(cfg *DbConfig, conn *sql.DB) error {
	var dialect, stmt string
	if "" == cfg.Dialect {
		dialect = cfg.Driver
	} else {
		dialect = cfg.Dialect
	}
	switch dialect {
	case "mysql":
		stmt = internal.DefaultMysqlTable()
	case "sqlite3":
		stmt = internal.DefaultSqliteTable()
	default:
		return errors.New("unsupported SQL dialect")
	}
	_, err := conn.Exec(stmt)
	return err
}

func BuildValues(data interface{}) (
	count int, args []any, failed []TxRecord, err error,
) {
	records, ok := data.([]interface{})
	if !ok {
		return 0, nil, nil, errors.New("invalid_records")
	}
	c := len(records)
	args = make([]any, c*numColumns)
	var e error
	hasher.New()
	for _, d := range records {
		rec, ok := d.(TxRecord)
		if !ok {
			err = fmt.Errorf("invalid record: %#v", d)
			failed = append(failed, TxRecord{})
			continue
		}
		if e = uuid.New(); nil != e {
			err = fmt.Errorf("error generating UUID: %w", e)
			failed = append(failed, rec)
			continue
		}
		idx := count * numColumns
		if args[idx], e = uuid.MarshalBinary(); nil != e {
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
		args[idx+1] = fmt.Sprintf("%016x", hasher.Hash())
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
			log.Errorf("error building values: %v", err)
			for _, f := range fails {
				_, err = fmt.Fprintf(failed, "%#v;\n", f)
				if nil != err {
					log.Errorf("can't log fails: %s", err.Error())
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

func ConnectDB(cfg *DbConfig) (*sql.DB, error) {
	if "" == cfg.Driver {
		return nil, errors.New("invalid DB driver")
	}
	if "" == cfg.Dsn {
		return nil, errors.New("invalid DSN")
	}
	conn, err := sql.Open(cfg.Driver, cfg.Dsn)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
