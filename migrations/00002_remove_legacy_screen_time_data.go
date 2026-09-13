package migrations

import (
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigration(upRemoveLegacyScreenTimeData, nil)
}

func upRemoveLegacyScreenTimeData(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(heartbeats)`)
	if err != nil {
		return err
	}
	legacyHeartbeats := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "date" || name == "active_seconds" {
			legacyHeartbeats = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	if legacyHeartbeats {
		if _, err := tx.Exec(`CREATE TABLE heartbeats_migrated (
			id INTEGER PRIMARY KEY,
			reported_at TEXT NOT NULL,
			device_id TEXT NOT NULL,
			user TEXT NOT NULL
		)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO heartbeats_migrated (id, reported_at, device_id, user)
			SELECT id, reported_at, device_id, user FROM heartbeats`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DROP TABLE heartbeats`); err != nil {
			return err
		}
		if _, err := tx.Exec(`ALTER TABLE heartbeats_migrated RENAME TO heartbeats`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`DROP TABLE IF EXISTS daily_totals`)
	return err
}
