package scheduler

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
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
// userExpr is the original expression in the user's timezone (stored for display).
// utcExpr is the UTC-converted expression (used for scheduling).
// If a schedule already exists for the chat, it is replaced.
func (s *Scheduler) AddSchedule(chatID int64, userExpr, utcExpr string) error {
	// Validate the UTC expression (what actually runs)
	if err := ValidateCron(utcExpr); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove existing entry if present
	if oldID, exists := s.entries[chatID]; exists {
		s.cron.Remove(oldID)
		delete(s.entries, chatID)
	}

	// Add the real cron job using the UTC expression
	entryID, err := s.cron.AddFunc(utcExpr, func() {
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

	// Persist both expressions to database
	if err := database.UpsertSchedule(s.db, chatID, userExpr, utcExpr); err != nil {
		// Roll back the cron entry if DB write fails
		s.cron.Remove(entryID)
		delete(s.entries, chatID)
		return fmt.Errorf("failed to persist schedule: %w", err)
	}

	next := s.cron.Entry(entryID).Next
	log.Printf("Added schedule for chat %d: %q (UTC: %q), next run: %s", chatID, userExpr, utcExpr, next.Format(time.RFC3339))
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
		utcExpr := sched.CronExpressionUTC
		entryID, err := s.cron.AddFunc(utcExpr, func() {
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
			log.Printf("Failed to load schedule for chat %d (%q): %v", chatID, utcExpr, err)
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

// ConvertCronToUTC converts a 5-field cron expression from a local timezone to UTC.
// offsetSecs is seconds east of UTC (e.g., ET winter = -18000, PT winter = -28800).
// Returns the UTC expression on success. If conversion is not possible (step patterns
// in hour field, inconsistent day boundaries), returns the original expression and an error.
func ConvertCronToUTC(expr string, offsetSecs int) (string, error) {
	offsetHours := offsetSecs / 3600
	if offsetHours == 0 {
		return expr, nil
	}

	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return expr, fmt.Errorf("expected 5-field cron expression, got %d fields", len(fields))
	}

	minute, hourField, dom, month, dowField := fields[0], fields[1], fields[2], fields[3], fields[4]

	// Wildcard or step-based hour: interval-based, no timezone conversion needed
	if hourField == "*" || strings.Contains(hourField, "/") {
		return expr, nil
	}

	newHourField, dayDelta, err := convertHourField(hourField, offsetHours)
	if err != nil {
		return expr, fmt.Errorf("cannot convert hour field: %w", err)
	}

	newDowField := dowField
	if dayDelta != 0 && dowField != "*" {
		newDowField, err = shiftDowField(dowField, dayDelta)
		if err != nil {
			return expr, fmt.Errorf("cannot shift day-of-week: %w", err)
		}
	}

	return strings.Join([]string{minute, newHourField, dom, month, newDowField}, " "), nil
}

// shiftHourValue converts a single local hour to UTC.
// Returns the UTC hour and the day delta (-1, 0, or +1).
func shiftHourValue(h, offsetHours int) (int, int) {
	utc := h - offsetHours
	if utc < 0 {
		return utc + 24, -1
	}
	if utc >= 24 {
		return utc - 24, +1
	}
	return utc, 0
}

// convertHourField converts the hour field of a cron expression from local to UTC.
// Supports single values (9), comma-separated lists (8,17), and ranges (9-17).
// Step patterns are handled by the caller before this is called.
// Returns the new hour field, the day delta (consistent across all hours), and any error.
func convertHourField(hourField string, offsetHours int) (string, int, error) {
	// Range (e.g., "9-17") — no commas
	if strings.Contains(hourField, "-") && !strings.Contains(hourField, ",") {
		parts := strings.SplitN(hourField, "-", 2)
		start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			return "", 0, fmt.Errorf("invalid hour range %q", hourField)
		}
		utcStart, deltaStart := shiftHourValue(start, offsetHours)
		utcEnd, deltaEnd := shiftHourValue(end, offsetHours)
		if deltaStart != deltaEnd {
			return "", 0, fmt.Errorf("hour range %q crosses midnight boundary after timezone conversion", hourField)
		}
		return fmt.Sprintf("%d-%d", utcStart, utcEnd), deltaStart, nil
	}

	// Single value or comma-separated list
	parts := strings.Split(hourField, ",")
	dayDelta := 0
	utcParts := make([]string, 0, len(parts))
	for i, p := range parts {
		h, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return "", 0, fmt.Errorf("invalid hour value %q", p)
		}
		utcH, delta := shiftHourValue(h, offsetHours)
		utcParts = append(utcParts, strconv.Itoa(utcH))
		if i == 0 {
			dayDelta = delta
		} else if delta != dayDelta {
			return "", 0, fmt.Errorf("hour values in %q cross different day boundaries after timezone conversion", hourField)
		}
	}
	return strings.Join(utcParts, ","), dayDelta, nil
}

// shiftDowField shifts the day-of-week field by delta (-1 or +1).
// Supports single values (1), comma-separated lists (1,3,5), and ranges (1-5).
func shiftDowField(dowField string, delta int) (string, error) {
	// Range (e.g., "1-5") — no commas
	if strings.Contains(dowField, "-") && !strings.Contains(dowField, ",") {
		parts := strings.SplitN(dowField, "-", 2)
		start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			return "", fmt.Errorf("invalid dow range %q", dowField)
		}
		newStart := ((start + delta) % 7 + 7) % 7
		newEnd := ((end + delta) % 7 + 7) % 7
		if newStart > newEnd {
			return "", fmt.Errorf("day-of-week range %q wraps around after shift", dowField)
		}
		return fmt.Sprintf("%d-%d", newStart, newEnd), nil
	}

	// Single value or comma-separated
	parts := strings.Split(dowField, ",")
	newParts := make([]string, 0, len(parts))
	for _, p := range parts {
		d, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return "", fmt.Errorf("invalid dow value %q", p)
		}
		newD := ((d + delta) % 7 + 7) % 7
		newParts = append(newParts, strconv.Itoa(newD))
	}
	return strings.Join(newParts, ","), nil
}
