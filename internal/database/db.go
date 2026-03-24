package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

CREATE TABLE IF NOT EXISTS user_config (
    chat_id         INTEGER PRIMARY KEY,
    station_code    TEXT    NOT NULL DEFAULT 'KBWI',
    city            TEXT    NOT NULL DEFAULT 'Baltimore',
    state           TEXT    NOT NULL DEFAULT 'MD',
    timezone_name   TEXT    NOT NULL DEFAULT 'ET',
    timezone_offset INTEGER NOT NULL DEFAULT -14400
);
`

// Schedule represents a row in the schedules table.
type Schedule struct {
	ID                int
	ChatID            int64
	CronExpression    string // user's original expression (for display)
	CronExpressionUTC string // UTC-converted expression (for scheduling)
	CreatedAt         string
	LastRun           *string
}

// UserConfig represents a row in the user_config table.
type UserConfig struct {
	ChatID         int64
	StationCode    string
	City           string
	State          string
	TimezoneName   string
	TimezoneOffset int // seconds east of UTC
}

// DefaultConfig returns the default weather configuration (Baltimore, MD).
func DefaultConfig() UserConfig {
	return UserConfig{
		StationCode:    "KBWI",
		City:           "Baltimore",
		State:          "MD",
		TimezoneName:   "ET",
		TimezoneOffset: -14400, // -4 hours in seconds
	}
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

	// Migration: add cron_expression_utc column (added in session 4)
	if _, err := db.Exec(`ALTER TABLE schedules ADD COLUMN cron_expression_utc TEXT`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("failed to migrate schedules schema: %w", err)
		}
	}
	// Backfill: existing rows pre-date timezone conversion, so their expression is already UTC
	if _, err := db.Exec(`UPDATE schedules SET cron_expression_utc = cron_expression WHERE cron_expression_utc IS NULL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to backfill cron_expression_utc: %w", err)
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
// userExpr is the original expression in the user's timezone (for display).
// utcExpr is the UTC-converted expression (for scheduling).
func UpsertSchedule(db *sql.DB, chatID int64, userExpr, utcExpr string) error {
	_, err := db.Exec(
		`INSERT INTO schedules (chat_id, cron_expression, cron_expression_utc)
		 VALUES (?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		     cron_expression     = excluded.cron_expression,
		     cron_expression_utc = excluded.cron_expression_utc,
		     last_run = NULL`,
		chatID, userExpr, utcExpr,
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
		"SELECT id, chat_id, cron_expression, cron_expression_utc, created_at, last_run FROM schedules WHERE chat_id = ?",
		chatID,
	).Scan(&s.ID, &s.ChatID, &s.CronExpression, &s.CronExpressionUTC, &s.CreatedAt, &s.LastRun)

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
	rows, err := db.Query("SELECT id, chat_id, cron_expression, cron_expression_utc, created_at, last_run FROM schedules")
	if err != nil {
		return nil, fmt.Errorf("failed to list schedules: %w", err)
	}
	defer rows.Close()

	var schedules []Schedule
	for rows.Next() {
		var s Schedule
		if err := rows.Scan(&s.ID, &s.ChatID, &s.CronExpression, &s.CronExpressionUTC, &s.CreatedAt, &s.LastRun); err != nil {
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

// GetUserConfig retrieves the config for the given chat.
// If no config exists, returns the default config.
func GetUserConfig(db *sql.DB, chatID int64) (UserConfig, error) {
	var c UserConfig
	err := db.QueryRow(
		"SELECT chat_id, station_code, city, state, timezone_name, timezone_offset FROM user_config WHERE chat_id = ?",
		chatID,
	).Scan(&c.ChatID, &c.StationCode, &c.City, &c.State, &c.TimezoneName, &c.TimezoneOffset)

	if err == sql.ErrNoRows {
		cfg := DefaultConfig()
		cfg.ChatID = chatID
		return cfg, nil
	}
	if err != nil {
		return UserConfig{}, fmt.Errorf("failed to get user config: %w", err)
	}
	return c, nil
}

// HasUserConfig returns true if the given chat has an explicitly saved config row.
// A missing row means the user has never run /weather-config, so timezone is not configured.
func HasUserConfig(db *sql.DB, chatID int64) (bool, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM user_config WHERE chat_id = ?", chatID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check user config: %w", err)
	}
	return count > 0, nil
}

// UpsertUserConfig inserts or updates the config for the given chat.
func UpsertUserConfig(db *sql.DB, cfg UserConfig) error {
	_, err := db.Exec(
		`INSERT INTO user_config (chat_id, station_code, city, state, timezone_name, timezone_offset)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		     station_code    = excluded.station_code,
		     city            = excluded.city,
		     state           = excluded.state,
		     timezone_name   = excluded.timezone_name,
		     timezone_offset = excluded.timezone_offset`,
		cfg.ChatID, cfg.StationCode, cfg.City, cfg.State, cfg.TimezoneName, cfg.TimezoneOffset,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert user config: %w", err)
	}
	return nil
}
