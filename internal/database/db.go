package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS schedules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id         INTEGER NOT NULL UNIQUE,
    cron_expression TEXT    NOT NULL,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    last_run        TEXT
);
`

// Schedule represents a row in the schedules table.
type Schedule struct {
	ID             int
	ChatID         int64
	CronExpression string
	CreatedAt      string
	LastRun        *string
}

// Open opens (or creates) the SQLite database at the given path,
// creates the parent directory if needed, and applies the schema.
func Open(dbPath string) (*sql.DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory %q: %w", dir, err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply schema: %w", err)
	}

	log.Println("Database initialized successfully")
	return db, nil
}

// Close closes the database connection.
func Close(db *sql.DB) {
	if db != nil {
		db.Close()
	}
}

// UpsertSchedule inserts a new schedule or updates the existing one for the given chat.
func UpsertSchedule(db *sql.DB, chatID int64, cronExpr string) error {
	_, err := db.Exec(
		`INSERT INTO schedules (chat_id, cron_expression)
		 VALUES (?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		     cron_expression = excluded.cron_expression,
		     last_run = NULL`,
		chatID, cronExpr,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert schedule: %w", err)
	}
	return nil
}

// RemoveSchedule deletes the schedule for the given chat.
// Returns an error if no schedule exists.
func RemoveSchedule(db *sql.DB, chatID int64) error {
	result, err := db.Exec("DELETE FROM schedules WHERE chat_id = ?", chatID)
	if err != nil {
		return fmt.Errorf("failed to remove schedule: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("no schedule found for chat %d", chatID)
	}

	return nil
}

// GetSchedule retrieves the schedule for the given chat.
func GetSchedule(db *sql.DB, chatID int64) (Schedule, error) {
	var s Schedule
	err := db.QueryRow(
		"SELECT id, chat_id, cron_expression, created_at, last_run FROM schedules WHERE chat_id = ?",
		chatID,
	).Scan(&s.ID, &s.ChatID, &s.CronExpression, &s.CreatedAt, &s.LastRun)

	if err == sql.ErrNoRows {
		return Schedule{}, fmt.Errorf("no schedule found for chat %d", chatID)
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("failed to get schedule: %w", err)
	}

	return s, nil
}

// ListSchedules returns all schedules in the database.
func ListSchedules(db *sql.DB) ([]Schedule, error) {
	rows, err := db.Query("SELECT id, chat_id, cron_expression, created_at, last_run FROM schedules")
	if err != nil {
		return nil, fmt.Errorf("failed to list schedules: %w", err)
	}
	defer rows.Close()

	var schedules []Schedule
	for rows.Next() {
		var s Schedule
		if err := rows.Scan(&s.ID, &s.ChatID, &s.CronExpression, &s.CreatedAt, &s.LastRun); err != nil {
			return nil, fmt.Errorf("failed to scan schedule: %w", err)
		}
		schedules = append(schedules, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating schedules: %w", err)
	}

	return schedules, nil
}

// UpdateLastRun stamps the last_run time for the given chat.
func UpdateLastRun(db *sql.DB, chatID int64, t time.Time) error {
	_, err := db.Exec(
		"UPDATE schedules SET last_run = ? WHERE chat_id = ?",
		t.UTC().Format(time.RFC3339), chatID,
	)
	if err != nil {
		return fmt.Errorf("failed to update last_run: %w", err)
	}
	return nil
}
