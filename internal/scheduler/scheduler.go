package scheduler

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"goober-bot/internal/database"
)

// WeatherSender is a callback that sends weather to a chat.
// It is provided by the caller (typically wraps weather.SendWeatherToChat).
type WeatherSender func(chatID int64) error

// Scheduler manages per-chat cron jobs for recurring weather updates.
type Scheduler struct {
	cron    *cron.Cron
	entries map[int64]cron.EntryID // chatID -> cron entry
	mu      sync.Mutex
	db      *sql.DB
	sender  WeatherSender
}

// cronParser is the standard 5-field cron parser (Minute Hour Dom Month Dow).
var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// New creates a new Scheduler.
func New(db *sql.DB, sender WeatherSender) *Scheduler {
	c := cron.New(
		cron.WithParser(cronParser),
		cron.WithLocation(time.UTC),
	)
	return &Scheduler{
		cron:    c,
		entries: make(map[int64]cron.EntryID),
		db:      db,
		sender:  sender,
	}
}

// Start begins running scheduled jobs.
func (s *Scheduler) Start() {
	s.cron.Start()
	log.Println("Scheduler started")
}

// Stop halts the scheduler and waits for running jobs to finish.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	log.Println("Scheduler stopped")
}

// MinInterval is the minimum allowed interval between cron firings.
const MinInterval = 15 * time.Minute

// ValidateCron checks whether a cron expression is valid and does not fire
// more frequently than MinInterval.
// Returns nil on success, an error describing the problem on failure.
func ValidateCron(expr string) error {
	sched, err := cronParser.Parse(expr)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}

	if err := checkMinInterval(sched); err != nil {
		return err
	}
	return nil
}

// checkMinInterval samples 5 consecutive firings from a reference time and
// rejects the expression if any gap is shorter than MinInterval.
func checkMinInterval(sched cron.Schedule) error {
	// Use a fixed reference to make the check deterministic
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prev := sched.Next(ref)
	for i := 0; i < 5; i++ {
		next := sched.Next(prev)
		if next.Sub(prev) < MinInterval {
			return fmt.Errorf("schedule fires too frequently (every %s); minimum interval is %s",
				next.Sub(prev).Truncate(time.Second), MinInterval)
		}
		prev = next
	}
	return nil
}

// NextRun returns the next scheduled time (in UTC) for the given cron expression.
func NextRun(expr string) (time.Time, error) {
	sched, err := cronParser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression: %w", err)
	}
	return sched.Next(time.Now().UTC()), nil
}

// FormatNextRun returns the next scheduled time formatted in the given timezone.
// tzName is the display label (e.g., "ET") and tzOffsetSecs is seconds east of UTC.
func FormatNextRun(expr string, tzName string, tzOffsetSecs int) (string, error) {
	next, err := NextRun(expr)
	if err != nil {
		return "unknown", err
	}
	tz := time.FixedZone(tzName, tzOffsetSecs)
	return next.In(tz).Format("Mon Jan 2 3:04 PM") + " " + tzName, nil
}

// FormatTimeInTZ formats a time string (RFC3339 or similar) in the given timezone.
// Returns the formatted string or falls back to the original on error.
func FormatTimeInTZ(timeStr string, tzName string, tzOffsetSecs int) string {
	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		// Try a looser parse
		t, err = time.Parse("2006-01-02T15:04:05Z", timeStr)
		if err != nil {
			return timeStr // give back the original if we can't parse
		}
	}
	tz := time.FixedZone(tzName, tzOffsetSecs)
	return t.In(tz).Format("Mon Jan 2 3:04 PM") + " " + tzName
}

// AddSchedule registers a cron job for the given chat and persists it to the database.
// If a schedule already exists for the chat, it is replaced.
func (s *Scheduler) AddSchedule(chatID int64, cronExpr string) error {
	// Validate first, before touching any state
	if err := ValidateCron(cronExpr); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove existing entry if present
	if oldID, exists := s.entries[chatID]; exists {
		s.cron.Remove(oldID)
		delete(s.entries, chatID)
	}

	// Add the real cron job
	entryID, err := s.cron.AddFunc(cronExpr, func() {
		log.Printf("Cron firing for chat %d", chatID)
		if err := s.sender(chatID); err != nil {
			log.Printf("Failed to send scheduled weather for chat %d: %v", chatID, err)
			return
		}
		// Update last_run in database
		if err := database.UpdateLastRun(s.db, chatID, time.Now()); err != nil {
			log.Printf("Failed to update last_run for chat %d: %v", chatID, err)
		}
	})
	if err != nil {
		return fmt.Errorf("failed to schedule cron job: %w", err)
	}

	// Store the entry ID so we can remove it later
	s.entries[chatID] = entryID

	// Persist to database
	if err := database.UpsertSchedule(s.db, chatID, cronExpr); err != nil {
		// Roll back the cron entry if DB write fails
		s.cron.Remove(entryID)
		delete(s.entries, chatID)
		return fmt.Errorf("failed to persist schedule: %w", err)
	}

	next := s.cron.Entry(entryID).Next
	log.Printf("Added schedule for chat %d: %q, next run: %s", chatID, cronExpr, next.Format(time.RFC3339))
	return nil
}

// RemoveSchedule cancels the cron job for the given chat and removes it from the database.
func (s *Scheduler) RemoveSchedule(chatID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entryID, exists := s.entries[chatID]
	if !exists {
		return fmt.Errorf("no schedule found for chat %d", chatID)
	}

	s.cron.Remove(entryID)
	delete(s.entries, chatID)

	if err := database.RemoveSchedule(s.db, chatID); err != nil {
		log.Printf("Warning: cron entry removed but DB delete failed for chat %d: %v", chatID, err)
		return err
	}

	log.Printf("Removed schedule for chat %d", chatID)
	return nil
}

// LoadFromDB reads all schedules from the database and registers cron jobs for each.
// This should be called once at startup to restore schedules from a previous run.
func (s *Scheduler) LoadFromDB() error {
	schedules, err := database.ListSchedules(s.db)
	if err != nil {
		return fmt.Errorf("failed to load schedules: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	loaded := 0
	for _, sched := range schedules {
		chatID := sched.ChatID // capture loop variable for closure
		cronExpr := sched.CronExpression
		entryID, err := s.cron.AddFunc(cronExpr, func() {
			log.Printf("Cron firing for chat %d", chatID)
			if err := s.sender(chatID); err != nil {
				log.Printf("Failed to send scheduled weather for chat %d: %v", chatID, err)
				return
			}
			if err := database.UpdateLastRun(s.db, chatID, time.Now()); err != nil {
				log.Printf("Failed to update last_run for chat %d: %v", chatID, err)
			}
		})
		if err != nil {
			log.Printf("Failed to load schedule for chat %d (%q): %v", chatID, cronExpr, err)
			continue
		}
		s.entries[chatID] = entryID
		loaded++
	}

	log.Printf("Loaded %d schedule(s) from database", loaded)
	return nil
}

// HasSchedule returns true if the given chat has an active schedule.
func (s *Scheduler) HasSchedule(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.entries[chatID]
	return exists
}

// Count returns the number of active schedules.
func (s *Scheduler) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
