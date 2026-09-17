package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenRepositoryMigratesLegacySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE heartbeats (
		id INTEGER PRIMARY KEY,
		reported_at TEXT NOT NULL,
		date TEXT NOT NULL,
		device_id TEXT NOT NULL,
		user TEXT NOT NULL,
		active_seconds INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE daily_totals (
		user TEXT NOT NULL,
		date TEXT NOT NULL,
		total_seconds INTEGER NOT NULL,
		PRIMARY KEY (user, date)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO heartbeats (id, reported_at, date, device_id, user, active_seconds)
		VALUES (7, '2026-09-13T12:00:00+02:00', '2026-09-13', 'pc-barn1', 'barn1', 30)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	repository, err := openRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.close() })

	rows, err := repository.db.Query(`PRAGMA table_info(heartbeats)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if columns["date"] || columns["active_seconds"] || !columns["reported_at"] || !columns["device_id"] || !columns["user"] {
		t.Fatalf("heartbeat columns = %#v", columns)
	}

	var id int
	var user string
	if err := repository.db.QueryRow(`SELECT id, user FROM heartbeats`).Scan(&id, &user); err != nil {
		t.Fatal(err)
	}
	if id != 7 || user != "barn1" {
		t.Fatalf("migrated heartbeat = %d, %q", id, user)
	}
	if _, err := repository.db.Exec(`SELECT * FROM daily_totals`); err == nil {
		t.Fatal("daily_totals still exists")
	}
	var version int
	if err := repository.db.QueryRow(`SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY version_id DESC LIMIT 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 3 {
		t.Fatalf("migration version = %d, want at least 3", version)
	}
}
