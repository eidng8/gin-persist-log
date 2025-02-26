package internal

func DefaultSqliteTable() string {
	//goland:noinspection SqlNoDataSourceInspection
	return `
		CREATE TABLE IF NOT EXISTS tx_log (
			id BYTEA PRIMARY KEY,
			req_hash BYTEA NOT NULL,
			headers TEXT NOT NULL,
			body BYTEA,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS ix_tx_log_hash ON tx_log (req_hash);`
}
