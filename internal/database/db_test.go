package database

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openTestDB creates an in-memory SQLite database with the schema applied.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesSchema(t *testing.T) {
	db := openTestDB(t)

	// Verify the schedules table exists by querying it
	_, err := db.Query("SELECT id, chat_id, cron_expression, cron_expression_utc, created_at, last_run FROM schedules")
	if err != nil {
		t.Fatalf("schedules table not created: %v", err)
	}
}

func TestUpsertScheduleInsert(t *testing.T) {
	db := openTestDB(t)

	err := UpsertSchedule(db, 100, "0 9 * * 1-5", "0 9 * * 1-5")
	if err != nil {
		t.Fatalf("UpsertSchedule failed: %v", err)
	}

	s, err := GetSchedule(db, 100)
	if err != nil {
		t.Fatalf("GetSchedule failed: %v", err)
	}
	if s.ChatID != 100 {
		t.Errorf("ChatID = %d, want 100", s.ChatID)
	}
	if s.CronExpression != "0 9 * * 1-5" {
		t.Errorf("CronExpression = %q, want %q", s.CronExpression, "0 9 * * 1-5")
	}
	if s.LastRun != nil {
		t.Errorf("LastRun should be nil for new schedule, got %v", s.LastRun)
	}
}

func TestUpsertScheduleUpdate(t *testing.T) {
	db := openTestDB(t)

	// Insert initial schedule
	if err := UpsertSchedule(db, 200, "0 9 * * 1-5", "0 9 * * 1-5"); err != nil {
		t.Fatalf("initial insert failed: %v", err)
	}

	// Update with new cron expression
	if err := UpsertSchedule(db, 200, "0 8 * * *", "0 13 * * *"); err != nil {
		t.Fatalf("upsert update failed: %v", err)
	}

	s, err := GetSchedule(db, 200)
	if err != nil {
		t.Fatalf("GetSchedule failed: %v", err)
	}
	if s.CronExpression != "0 8 * * *" {
		t.Errorf("CronExpression = %q, want %q", s.CronExpression, "0 8 * * *")
	}
	if s.CronExpressionUTC != "0 13 * * *" {
		t.Errorf("CronExpressionUTC = %q, want %q", s.CronExpressionUTC, "0 13 * * *")
	}

	// Verify only one row exists for this chat
	schedules, err := ListSchedules(db)
	if err != nil {
		t.Fatalf("ListSchedules failed: %v", err)
	}
	count := 0
	for _, sched := range schedules {
		if sched.ChatID == 200 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 schedule for chat 200, got %d", count)
	}
}

func TestUpsertScheduleResetsLastRun(t *testing.T) {
	db := openTestDB(t)

	// Insert and set last_run
	if err := UpsertSchedule(db, 300, "0 9 * * *", "0 9 * * *"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateLastRun(db, 300, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Verify last_run is set
	s, _ := GetSchedule(db, 300)
	if s.LastRun == nil {
		t.Fatal("LastRun should be set after UpdateLastRun")
	}

	// Upsert with new expression should reset last_run
	if err := UpsertSchedule(db, 300, "0 10 * * *", "0 15 * * *"); err != nil {
		t.Fatal(err)
	}
	s, _ = GetSchedule(db, 300)
	if s.LastRun != nil {
		t.Errorf("LastRun should be nil after upsert, got %v", s.LastRun)
	}
}

func TestRemoveSchedule(t *testing.T) {
	db := openTestDB(t)

	if err := UpsertSchedule(db, 400, "0 9 * * *", "0 9 * * *"); err != nil {
		t.Fatal(err)
	}

	if err := RemoveSchedule(db, 400); err != nil {
		t.Fatalf("RemoveSchedule failed: %v", err)
	}

	_, err := GetSchedule(db, 400)
	if err == nil {
		t.Error("GetSchedule should fail after removal")
	}
}

func TestRemoveScheduleNotFound(t *testing.T) {
	db := openTestDB(t)

	err := RemoveSchedule(db, 999)
	if err == nil {
		t.Error("RemoveSchedule should fail for non-existent chat")
	}
}

func TestGetScheduleNotFound(t *testing.T) {
	db := openTestDB(t)

	_, err := GetSchedule(db, 999)
	if err == nil {
		t.Error("GetSchedule should fail for non-existent chat")
	}
}

func TestListSchedules(t *testing.T) {
	db := openTestDB(t)

	// Empty list
	schedules, err := ListSchedules(db)
	if err != nil {
		t.Fatalf("ListSchedules failed: %v", err)
	}
	if len(schedules) != 0 {
		t.Errorf("expected 0 schedules, got %d", len(schedules))
	}

	// Add some schedules
	if err := UpsertSchedule(db, 500, "0 9 * * *", "0 9 * * *"); err != nil {
		t.Fatal(err)
	}
	if err := UpsertSchedule(db, 501, "0 10 * * *", "0 10 * * *"); err != nil {
		t.Fatal(err)
	}
	if err := UpsertSchedule(db, 502, "0 9 * * *", "0 9 * * *"); err != nil {
		t.Fatal(err)
	}

	schedules, err = ListSchedules(db)
	if err != nil {
		t.Fatalf("ListSchedules failed: %v", err)
	}
	if len(schedules) != 3 {
		t.Errorf("expected 3 schedules, got %d", len(schedules))
	}
}

func TestDuplicateCronExpressionsAllowed(t *testing.T) {
	db := openTestDB(t)

	// Two different chats with the same cron expression should both succeed
	if err := UpsertSchedule(db, 600, "0 9 * * 1-5", "0 9 * * 1-5"); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}
	if err := UpsertSchedule(db, 601, "0 9 * * 1-5", "0 9 * * 1-5"); err != nil {
		t.Fatalf("second insert with same cron should succeed but failed: %v", err)
	}

	schedules, err := ListSchedules(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 2 {
		t.Errorf("expected 2 schedules, got %d", len(schedules))
	}
}

func TestUpdateLastRun(t *testing.T) {
	db := openTestDB(t)

	if err := UpsertSchedule(db, 700, "0 9 * * *", "0 9 * * *"); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC)
	if err := UpdateLastRun(db, 700, now); err != nil {
		t.Fatalf("UpdateLastRun failed: %v", err)
	}

	s, err := GetSchedule(db, 700)
	if err != nil {
		t.Fatal(err)
	}
	if s.LastRun == nil {
		t.Fatal("LastRun should be set")
	}
	if *s.LastRun != now.Format(time.RFC3339) {
		t.Errorf("LastRun = %q, want %q", *s.LastRun, now.Format(time.RFC3339))
	}
}

// --- UserConfig tests ---

func TestGetUserConfigDefault(t *testing.T) {
	db := openTestDB(t)

	cfg, err := GetUserConfig(db, 800)
	if err != nil {
		t.Fatalf("GetUserConfig failed: %v", err)
	}

	def := DefaultConfig()
	if cfg.StationCode != def.StationCode {
		t.Errorf("StationCode = %q, want default %q", cfg.StationCode, def.StationCode)
	}
	if cfg.City != def.City {
		t.Errorf("City = %q, want default %q", cfg.City, def.City)
	}
	if cfg.State != def.State {
		t.Errorf("State = %q, want default %q", cfg.State, def.State)
	}
	if cfg.TimezoneName != def.TimezoneName {
		t.Errorf("TimezoneName = %q, want default %q", cfg.TimezoneName, def.TimezoneName)
	}
	if cfg.TimezoneOffset != def.TimezoneOffset {
		t.Errorf("TimezoneOffset = %d, want default %d", cfg.TimezoneOffset, def.TimezoneOffset)
	}
	if cfg.ChatID != 800 {
		t.Errorf("ChatID = %d, want 800", cfg.ChatID)
	}
}

func TestUpsertUserConfig(t *testing.T) {
	db := openTestDB(t)

	cfg := UserConfig{
		ChatID:         900,
		StationCode:    "KJFK",
		City:           "New York",
		State:          "NY",
		TimezoneName:   "ET",
		TimezoneOffset: -18000,
	}

	if err := UpsertUserConfig(db, cfg); err != nil {
		t.Fatalf("UpsertUserConfig failed: %v", err)
	}

	got, err := GetUserConfig(db, 900)
	if err != nil {
		t.Fatalf("GetUserConfig failed: %v", err)
	}
	if got.StationCode != "KJFK" {
		t.Errorf("StationCode = %q, want %q", got.StationCode, "KJFK")
	}
	if got.City != "New York" {
		t.Errorf("City = %q, want %q", got.City, "New York")
	}
	if got.State != "NY" {
		t.Errorf("State = %q, want %q", got.State, "NY")
	}
	if got.TimezoneOffset != -18000 {
		t.Errorf("TimezoneOffset = %d, want %d", got.TimezoneOffset, -18000)
	}
}

func TestUpsertUserConfigUpdate(t *testing.T) {
	db := openTestDB(t)

	cfg := UserConfig{
		ChatID:         1000,
		StationCode:    "KJFK",
		City:           "New York",
		State:          "NY",
		TimezoneName:   "ET",
		TimezoneOffset: -18000,
	}
	if err := UpsertUserConfig(db, cfg); err != nil {
		t.Fatal(err)
	}

	// Update just the station
	cfg.StationCode = "KLGA"
	cfg.City = "Queens"
	if err := UpsertUserConfig(db, cfg); err != nil {
		t.Fatal(err)
	}

	got, err := GetUserConfig(db, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got.StationCode != "KLGA" {
		t.Errorf("StationCode = %q, want %q", got.StationCode, "KLGA")
	}
	if got.City != "Queens" {
		t.Errorf("City = %q, want %q", got.City, "Queens")
	}
	// State should still be NY
	if got.State != "NY" {
		t.Errorf("State = %q, want %q", got.State, "NY")
	}
}

func TestHasUserConfigFalseWhenMissing(t *testing.T) {
	db := openTestDB(t)

	has, err := HasUserConfig(db, 9999)
	if err != nil {
		t.Fatalf("HasUserConfig failed: %v", err)
	}
	if has {
		t.Error("HasUserConfig should return false for unconfigured chat")
	}
}

func TestHasUserConfigTrueAfterUpsert(t *testing.T) {
	db := openTestDB(t)

	cfg := UserConfig{ChatID: 9998, StationCode: "KJFK", City: "New York", State: "NY", TimezoneName: "ET", TimezoneOffset: -18000}
	if err := UpsertUserConfig(db, cfg); err != nil {
		t.Fatal(err)
	}

	has, err := HasUserConfig(db, 9998)
	if err != nil {
		t.Fatalf("HasUserConfig failed: %v", err)
	}
	if !has {
		t.Error("HasUserConfig should return true after UpsertUserConfig")
	}
}

func TestUserConfigIndependentPerChat(t *testing.T) {
	db := openTestDB(t)

	cfg1 := UserConfig{ChatID: 1100, StationCode: "KJFK", City: "New York", State: "NY", TimezoneName: "ET", TimezoneOffset: -18000}
	cfg2 := UserConfig{ChatID: 1101, StationCode: "KLAX", City: "Los Angeles", State: "CA", TimezoneName: "PT", TimezoneOffset: -28800}

	if err := UpsertUserConfig(db, cfg1); err != nil {
		t.Fatal(err)
	}
	if err := UpsertUserConfig(db, cfg2); err != nil {
		t.Fatal(err)
	}

	got1, _ := GetUserConfig(db, 1100)
	got2, _ := GetUserConfig(db, 1101)

	if got1.StationCode != "KJFK" || got2.StationCode != "KLAX" {
		t.Errorf("configs should be independent: got %q and %q", got1.StationCode, got2.StationCode)
	}
}
