package scheduler

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goober-bot/internal/database"
)

// newTestScheduler creates a scheduler backed by an in-memory DB
// and a sender that records which chatIDs were called.
func newTestScheduler(t *testing.T) (*Scheduler, *sync.Map) {
	t.Helper()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	sent := &sync.Map{} // chatID -> call count (int64)
	sender := func(chatID int64) error {
		val, _ := sent.LoadOrStore(chatID, new(int64))
		atomic.AddInt64(val.(*int64), 1)
		return nil
	}

	s := New(db, sender)
	t.Cleanup(func() { s.Stop() })

	return s, sent
}

func TestValidateCron(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{"valid weekday 9am", "0 9 * * 1-5", false},
		{"valid daily 8am", "0 8 * * *", false},
		{"valid every 15 min", "*/15 * * * *", false},
		{"valid every 30 min", "0,30 * * * *", false},
		{"valid complex", "30 6,18 * * 1-5", false},
		{"too frequent every minute", "* * * * *", true},
		{"too frequent every 5 min", "*/5 * * * *", true},
		{"too frequent every 10 min", "*/10 * * * *", true},
		{"too frequent every 14 min", "0,14,28,42,56 * * * *", true},
		{"invalid empty", "", true},
		{"invalid garbage", "not a cron", true},
		{"invalid too few fields", "0 9 *", true},
		{"invalid too many fields", "0 0 9 * * 1-5", true}, // 6-field (with seconds) should fail
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCron(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCron(%q) error = %v, wantErr %v", tt.expr, err, tt.wantErr)
			}
		})
	}
}

func TestValidateCronRateLimitMessage(t *testing.T) {
	err := ValidateCron("* * * * *")
	if err == nil {
		t.Fatal("expected error for every-minute cron")
	}
	// Error message should mention the frequency and the minimum
	errStr := err.Error()
	if !containsStr(errStr, "too frequently") {
		t.Errorf("error message should mention 'too frequently', got: %s", errStr)
	}
	if !containsStr(errStr, "15m") {
		t.Errorf("error message should mention '15m' minimum, got: %s", errStr)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestNextRun(t *testing.T) {
	next, err := NextRun("0 9 * * *")
	if err != nil {
		t.Fatalf("NextRun failed: %v", err)
	}
	if next.Before(time.Now().UTC()) {
		t.Errorf("NextRun returned a time in the past: %v", next)
	}
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Errorf("NextRun expected 09:00, got %02d:%02d", next.Hour(), next.Minute())
	}
}

func TestNextRunInvalid(t *testing.T) {
	_, err := NextRun("garbage")
	if err == nil {
		t.Error("NextRun should fail for invalid expression")
	}
}

func TestAddSchedule(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.Start()

	err := s.AddSchedule(100, "0 9 * * 1-5")
	if err != nil {
		t.Fatalf("AddSchedule failed: %v", err)
	}

	if !s.HasSchedule(100) {
		t.Error("HasSchedule(100) should be true")
	}
	if s.Count() != 1 {
		t.Errorf("Count() = %d, want 1", s.Count())
	}

	// Verify persisted to database
	sched, err := database.GetSchedule(s.db, 100)
	if err != nil {
		t.Fatalf("schedule not persisted to DB: %v", err)
	}
	if sched.CronExpression != "0 9 * * 1-5" {
		t.Errorf("DB cron = %q, want %q", sched.CronExpression, "0 9 * * 1-5")
	}
}

func TestAddScheduleInvalidCron(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.Start()

	err := s.AddSchedule(100, "not valid")
	if err == nil {
		t.Error("AddSchedule should fail for invalid cron")
	}
	if s.HasSchedule(100) {
		t.Error("HasSchedule should be false after failed add")
	}
}

func TestAddScheduleReplacesExisting(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.Start()

	if err := s.AddSchedule(200, "0 9 * * *"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSchedule(200, "0 10 * * *"); err != nil {
		t.Fatal(err)
	}

	if s.Count() != 1 {
		t.Errorf("Count() = %d, want 1 (should replace, not add)", s.Count())
	}

	// DB should have the new expression
	sched, err := database.GetSchedule(s.db, 200)
	if err != nil {
		t.Fatal(err)
	}
	if sched.CronExpression != "0 10 * * *" {
		t.Errorf("DB cron = %q, want %q", sched.CronExpression, "0 10 * * *")
	}
}

func TestRemoveSchedule(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.Start()

	if err := s.AddSchedule(300, "0 9 * * *"); err != nil {
		t.Fatal(err)
	}

	if err := s.RemoveSchedule(300); err != nil {
		t.Fatalf("RemoveSchedule failed: %v", err)
	}

	if s.HasSchedule(300) {
		t.Error("HasSchedule should be false after removal")
	}
	if s.Count() != 0 {
		t.Errorf("Count() = %d, want 0", s.Count())
	}

	// DB should also be cleaned
	_, err := database.GetSchedule(s.db, 300)
	if err == nil {
		t.Error("schedule should be removed from DB")
	}
}

func TestRemoveScheduleNotFound(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.Start()

	err := s.RemoveSchedule(999)
	if err == nil {
		t.Error("RemoveSchedule should fail for non-existent chat")
	}
}

func TestLoadFromDB(t *testing.T) {
	// Set up DB with pre-existing schedules
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := database.UpsertSchedule(db, 400, "0 9 * * *"); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertSchedule(db, 401, "0 10 * * *"); err != nil {
		t.Fatal(err)
	}

	sender := func(chatID int64) error { return nil }
	s := New(db, sender)
	defer s.Stop()

	if err := s.LoadFromDB(); err != nil {
		t.Fatalf("LoadFromDB failed: %v", err)
	}

	s.Start()

	if s.Count() != 2 {
		t.Errorf("Count() = %d, want 2", s.Count())
	}
	if !s.HasSchedule(400) {
		t.Error("HasSchedule(400) should be true")
	}
	if !s.HasSchedule(401) {
		t.Error("HasSchedule(401) should be true")
	}
}

func TestCronEntryRegistered(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.Start()

	// Use every-15-minutes (the minimum allowed interval)
	err := s.AddSchedule(500, "*/15 * * * *")
	if err != nil {
		t.Fatalf("AddSchedule failed: %v", err)
	}

	// Verify the cron entry exists and has a reasonable next time
	s.mu.Lock()
	entryID := s.entries[500]
	s.mu.Unlock()

	entry := s.cron.Entry(entryID)
	if entry.Next.IsZero() {
		t.Error("cron entry has zero next time")
	}
}

func TestAddScheduleRateLimited(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.Start()

	// Every minute should be rejected
	err := s.AddSchedule(600, "* * * * *")
	if err == nil {
		t.Error("AddSchedule should reject every-minute cron")
	}
	if s.HasSchedule(600) {
		t.Error("no schedule should exist after rate-limited rejection")
	}

	// Every 15 minutes should be accepted
	err = s.AddSchedule(600, "*/15 * * * *")
	if err != nil {
		t.Fatalf("AddSchedule should accept every-15-min cron: %v", err)
	}
}
