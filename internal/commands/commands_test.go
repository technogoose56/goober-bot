package commands

import (
	"testing"

	"goober-bot/internal/database"
	"goober-bot/internal/scheduler"
)

// We can't easily mock tgbotapi.BotAPI since bot.Send requires a real HTTP client.
// Instead, we test the command routing (HandleCommand dispatch) and the underlying
// scheduler/database interactions. The actual reply formatting is validated by
// integration-level tests or manual testing.

func TestHandleCommandRouting(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		handled bool
	}{
		{"recurring-weather with args", "/recurring-weather 0 9 * * 1-5", true},
		{"recurring-weather no args", "/recurring-weather", true},
		{"recurring-weather with space", "/recurring-weather ", true},
		{"cancel-weather", "/cancel-weather", true},
		{"weather-schedules", "/weather-schedules", true},
		{"plain weather", "/weather", false},
		{"hi", "/hi", false},
		{"unknown", "/something-else", false},
		{"empty", "", false},
		{"partial match", "/recurring-weathe", false},
		{"prefix attack", "/recurring-weatherX", false},
	}

	// We use a nil Deps since we're only testing routing, not execution.
	// HandleCommand will panic if it tries to use deps for unhandled commands,
	// but for handled ones we need real deps. So we skip execution for those
	// by just testing the return value.

	// For the routing test, we need to verify HandleCommand returns the right bool.
	// We can't call it with nil deps for handled commands because they'd nil-pointer.
	// So test handled=false cases directly, and handled=true cases via separate tests.
	for _, tt := range tests {
		if !tt.handled {
			t.Run(tt.name, func(t *testing.T) {
				got := HandleCommand(tt.text, 123, Deps{})
				if got != tt.handled {
					t.Errorf("HandleCommand(%q) = %v, want %v", tt.text, got, tt.handled)
				}
			})
		}
	}
}

func TestHandleCommandRoutingHandled(t *testing.T) {
	// Verify the commands that should be handled are correctly recognized.
	// Since we can't easily mock tgbotapi.BotAPI, we test that the functions
	// panic (due to nil bot) rather than returning false. A panic inside a
	// handler means it was dispatched correctly -- an unhandled command returns
	// false without panicking.
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sender := func(chatID int64) error { return nil }
	sched := scheduler.New(db, sender)
	sched.Start()
	defer sched.Stop()

	deps := Deps{
		Bot:       nil,
		Scheduler: sched,
		DB:        db,
	}

	handledCmds := []struct {
		name string
		text string
	}{
		{"recurring-weather with args", "/recurring-weather 0 9 * * 1-5"},
		{"recurring-weather no args", "/recurring-weather"},
		{"cancel-weather", "/cancel-weather"},
		{"weather-schedules", "/weather-schedules"},
	}

	for _, tt := range handledCmds {
		t.Run(tt.name, func(t *testing.T) {
			panicked := false
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
					}
				}()
				HandleCommand(tt.text, 123, deps)
			}()
			// A panic means the handler was invoked (and hit nil bot).
			// If it didn't panic and returned false, the command wasn't routed.
			if !panicked {
				t.Errorf("HandleCommand(%q) did not dispatch to a handler (expected panic from nil bot)", tt.text)
			}
		})
	}
}

func TestRecurringWeatherParsing(t *testing.T) {
	// Test that the cron expression is correctly extracted from the command text.
	// We do this by checking that AddSchedule is called with the correct expression.
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sender := func(chatID int64) error { return nil }
	sched := scheduler.New(db, sender)
	sched.Start()
	defer sched.Stop()

	deps := Deps{
		Bot:       nil,
		Scheduler: sched,
		DB:        db,
	}

	// Call handleRecurringWeather directly (will panic on reply but that's fine)
	func() {
		defer func() { recover() }()
		handleRecurringWeather("/recurring-weather 30 8 * * 1-5", 100, deps)
	}()

	// Verify the schedule was persisted with the correct expression
	schedule, err := database.GetSchedule(db, 100)
	if err != nil {
		t.Fatalf("schedule not created: %v", err)
	}
	if schedule.CronExpression != "30 8 * * 1-5" {
		t.Errorf("CronExpression = %q, want %q", schedule.CronExpression, "30 8 * * 1-5")
	}
}

func TestRecurringWeatherInvalidCron(t *testing.T) {
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sender := func(chatID int64) error { return nil }
	sched := scheduler.New(db, sender)
	sched.Start()
	defer sched.Stop()

	deps := Deps{
		Bot:       nil,
		Scheduler: sched,
		DB:        db,
	}

	// Invalid cron should not create a schedule
	func() {
		defer func() { recover() }()
		handleRecurringWeather("/recurring-weather invalid cron expression", 200, deps)
	}()

	_, err = database.GetSchedule(db, 200)
	if err == nil {
		t.Error("schedule should not be created for invalid cron expression")
	}
}

func TestCancelWeatherIntegration(t *testing.T) {
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sender := func(chatID int64) error { return nil }
	sched := scheduler.New(db, sender)
	sched.Start()
	defer sched.Stop()

	deps := Deps{
		Bot:       nil,
		Scheduler: sched,
		DB:        db,
	}

	// First add a schedule
	func() {
		defer func() { recover() }()
		handleRecurringWeather("/recurring-weather 0 9 * * *", 300, deps)
	}()

	// Verify it exists
	if !sched.HasSchedule(300) {
		t.Fatal("schedule should exist after adding")
	}

	// Cancel it
	func() {
		defer func() { recover() }()
		handleCancelWeather(300, deps)
	}()

	// Verify it's gone from both scheduler and DB
	if sched.HasSchedule(300) {
		t.Error("schedule should be removed from scheduler after cancel")
	}
	_, err = database.GetSchedule(db, 300)
	if err == nil {
		t.Error("schedule should be removed from DB after cancel")
	}
}
