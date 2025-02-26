package internal

func DefaultMysqlTable() string {
	//goland:noinspection SqlNoDataSourceInspection
	return `
		CREATE TABLE IF NOT EXISTS tx_log (
			id BINARY(16) NOT NULL PRIMARY KEY,
			req_hash BINARY(16) NOT NULL,
			headers TEXT NOT NULL,
			body BLOB,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			INDEX ix_tx_log_hash (req_hash)
		)`
}
