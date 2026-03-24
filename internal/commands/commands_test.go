package commands

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"goober-bot/internal/database"
	"goober-bot/internal/scheduler"
)

// mockSender records messages sent via the MessageSender interface.
type mockSender struct {
	mu       sync.Mutex
	messages []tgbotapi.MessageConfig
}

func (m *mockSender) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	if msg, ok := c.(tgbotapi.MessageConfig); ok {
		m.mu.Lock()
		m.messages = append(m.messages, msg)
		m.mu.Unlock()
	}
	return tgbotapi.Message{}, nil
}

func (m *mockSender) lastMessage() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return ""
	}
	return m.messages[len(m.messages)-1].Text
}

func (m *mockSender) messageCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

// newTestDeps creates a Deps with mock bot, in-memory DB, and real scheduler.
// Uses a mock HTTP server that returns 200 for station validation (valid station).
func newTestDeps(t *testing.T) (Deps, *mockSender) {
	t.Helper()
	return newTestDepsWithHTTPStatus(t, http.StatusOK)
}

// newTestDepsWithHTTPStatus creates test deps with a mock HTTP server
// that returns the given status code for all requests.
func newTestDepsWithHTTPStatus(t *testing.T, statusCode int) (Deps, *mockSender) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	sender := func(chatID int64) error { return nil }
	sched := scheduler.New(db, sender)
	sched.Start()
	t.Cleanup(func() { sched.Stop() })

	// Mock HTTP server for NOAA station validation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
	}))
	t.Cleanup(server.Close)
	httpClient := server.Client()
	httpClient.Transport = &rewriteTransport{base: httpClient.Transport, targetURL: server.URL}

	mock := &mockSender{}
	deps := Deps{Bot: mock, Scheduler: sched, DB: db, HTTPClient: httpClient}
	return deps, mock
}

// rewriteTransport rewrites outgoing request URLs to point at a local test server.
type rewriteTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = rt.targetURL[len("http://"):]
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

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
		{"weather-schedule", "/weather-schedule", true},
		{"weather-schedules plural alias", "/weather-schedules", true},
		{"weather-config no args", "/weather-config", true},
		{"weather-config with args", "/weather-config station KJFK", true},
		{"weather-config prefix attack", "/weather-configX", false},
		{"plain weather", "/weather", false},
		{"hi", "/hi", false},
		{"unknown", "/something-else", false},
		{"empty", "", false},
		{"partial match", "/recurring-weathe", false},
		{"prefix attack", "/recurring-weatherX", false},
	}

	deps, _ := newTestDeps(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HandleCommand(tt.text, 123, deps)
			if got != tt.handled {
				t.Errorf("HandleCommand(%q) = %v, want %v", tt.text, got, tt.handled)
			}
		})
	}
}

func TestRecurringWeatherParsing(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleRecurringWeather("/recurring-weather 30 8 * * 1-5", 100, deps)

	// Verify the schedule was persisted
	schedule, err := database.GetSchedule(deps.DB, 100)
	if err != nil {
		t.Fatalf("schedule not created: %v", err)
	}
	if schedule.CronExpression != "30 8 * * 1-5" {
		t.Errorf("CronExpression = %q, want %q", schedule.CronExpression, "30 8 * * 1-5")
	}

	// Verify a confirmation message was sent
	if mock.messageCount() == 0 {
		t.Fatal("expected a reply message")
	}
	last := mock.lastMessage()
	if !containsStr(last, "Schedule set") {
		t.Errorf("reply should contain 'Schedule set', got: %s", last)
	}
}

func TestRecurringWeatherNoArgs(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleRecurringWeather("/recurring-weather", 101, deps)

	if mock.messageCount() == 0 {
		t.Fatal("expected usage message")
	}
	if !containsStr(mock.lastMessage(), "Usage:") {
		t.Errorf("expected usage message, got: %s", mock.lastMessage())
	}
}

func TestRecurringWeatherInvalidCron(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleRecurringWeather("/recurring-weather invalid cron expression", 200, deps)

	_, err := database.GetSchedule(deps.DB, 200)
	if err == nil {
		t.Error("schedule should not be created for invalid cron expression")
	}

	if !containsStr(mock.lastMessage(), "Invalid cron expression") {
		t.Errorf("expected error message, got: %s", mock.lastMessage())
	}
}

func TestRecurringWeatherRateLimited(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleRecurringWeather("/recurring-weather * * * * *", 201, deps)

	_, err := database.GetSchedule(deps.DB, 201)
	if err == nil {
		t.Error("schedule should not be created for too-frequent cron")
	}

	if !containsStr(mock.lastMessage(), "too frequently") {
		t.Errorf("expected rate limit error, got: %s", mock.lastMessage())
	}
}

func TestCancelWeatherIntegration(t *testing.T) {
	deps, mock := newTestDeps(t)

	// First add a schedule
	handleRecurringWeather("/recurring-weather 0 9 * * *", 300, deps)

	if !deps.Scheduler.HasSchedule(300) {
		t.Fatal("schedule should exist after adding")
	}

	// Cancel it
	handleCancelWeather(300, deps)

	// Verify it's gone from both scheduler and DB
	if deps.Scheduler.HasSchedule(300) {
		t.Error("schedule should be removed from scheduler after cancel")
	}
	_, err := database.GetSchedule(deps.DB, 300)
	if err == nil {
		t.Error("schedule should be removed from DB after cancel")
	}

	if !containsStr(mock.lastMessage(), "cancelled") {
		t.Errorf("expected cancellation message, got: %s", mock.lastMessage())
	}
}

func TestCancelWeatherNoSchedule(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleCancelWeather(301, deps)

	if !containsStr(mock.lastMessage(), "No active schedule") {
		t.Errorf("expected 'no schedule' message, got: %s", mock.lastMessage())
	}
}

func TestWeatherScheduleWithSchedule(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleRecurringWeather("/recurring-weather 0 9 * * 1-5", 400, deps)
	handleWeatherSchedule(400, deps)

	last := mock.lastMessage()
	if !containsStr(last, "Active schedule") {
		t.Errorf("expected 'Active schedule' message, got: %s", last)
	}
	if !containsStr(last, "0 9 * * 1-5") {
		t.Errorf("expected cron expression in message, got: %s", last)
	}
}

func TestWeatherScheduleNoSchedule(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleWeatherSchedule(401, deps)

	if !containsStr(mock.lastMessage(), "No active schedule") {
		t.Errorf("expected 'no schedule' message, got: %s", mock.lastMessage())
	}
}

// --- Weather Config tests ---

func TestWeatherConfigShowDefault(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleWeatherConfig("/weather-config", 500, deps)

	last := mock.lastMessage()
	if !containsStr(last, "KBWI") {
		t.Errorf("expected default station KBWI, got: %s", last)
	}
	if !containsStr(last, "Baltimore") {
		t.Errorf("expected default city Baltimore, got: %s", last)
	}
}

func TestWeatherConfigSetStation(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleWeatherConfig("/weather-config station KJFK", 600, deps)

	cfg, err := database.GetUserConfig(deps.DB, 600)
	if err != nil {
		t.Fatalf("GetUserConfig failed: %v", err)
	}
	if cfg.StationCode != "KJFK" {
		t.Errorf("StationCode = %q, want %q", cfg.StationCode, "KJFK")
	}
	// City should remain at default
	if cfg.City != "Baltimore" {
		t.Errorf("City = %q, want default %q", cfg.City, "Baltimore")
	}

	if !containsStr(mock.lastMessage(), "KJFK") {
		t.Errorf("reply should mention KJFK, got: %s", mock.lastMessage())
	}
}

func TestWeatherConfigSetCity(t *testing.T) {
	deps, _ := newTestDeps(t)

	handleWeatherConfig("/weather-config city New York", 700, deps)

	cfg, _ := database.GetUserConfig(deps.DB, 700)
	if cfg.City != "New York" {
		t.Errorf("City = %q, want %q", cfg.City, "New York")
	}
}

func TestWeatherConfigSetState(t *testing.T) {
	deps, _ := newTestDeps(t)

	handleWeatherConfig("/weather-config state ny", 800, deps)

	cfg, _ := database.GetUserConfig(deps.DB, 800)
	if cfg.State != "NY" {
		t.Errorf("State = %q, want %q (should be uppercased)", cfg.State, "NY")
	}
}

func TestWeatherConfigSetTimezone(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleWeatherConfig("/weather-config timezone PT -8", 900, deps)

	cfg, _ := database.GetUserConfig(deps.DB, 900)
	if cfg.TimezoneName != "PT" {
		t.Errorf("TimezoneName = %q, want %q", cfg.TimezoneName, "PT")
	}
	if cfg.TimezoneOffset != -28800 {
		t.Errorf("TimezoneOffset = %d, want %d", cfg.TimezoneOffset, -28800)
	}

	if !containsStr(mock.lastMessage(), "PT") {
		t.Errorf("reply should mention PT, got: %s", mock.lastMessage())
	}
}

func TestWeatherConfigTimezoneInvalidOffset(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleWeatherConfig("/weather-config timezone PT abc", 901, deps)

	if !containsStr(mock.lastMessage(), "Invalid offset") {
		t.Errorf("expected invalid offset error, got: %s", mock.lastMessage())
	}
}

func TestWeatherConfigMultipleSettings(t *testing.T) {
	deps, _ := newTestDeps(t)

	handleWeatherConfig("/weather-config station KLAX", 1000, deps)
	handleWeatherConfig("/weather-config city Los Angeles", 1000, deps)
	handleWeatherConfig("/weather-config state CA", 1000, deps)

	cfg, err := database.GetUserConfig(deps.DB, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StationCode != "KLAX" {
		t.Errorf("StationCode = %q, want %q", cfg.StationCode, "KLAX")
	}
	if cfg.City != "Los Angeles" {
		t.Errorf("City = %q, want %q", cfg.City, "Los Angeles")
	}
	if cfg.State != "CA" {
		t.Errorf("State = %q, want %q", cfg.State, "CA")
	}
}

func TestWeatherConfigUnknownSetting(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleWeatherConfig("/weather-config foobar value", 1100, deps)

	if !containsStr(mock.lastMessage(), "Usage:") {
		t.Errorf("expected usage message for unknown setting, got: %s", mock.lastMessage())
	}
}

func TestWeatherConfigStationUppercased(t *testing.T) {
	deps, _ := newTestDeps(t)

	handleWeatherConfig("/weather-config station kjfk", 1200, deps)

	cfg, _ := database.GetUserConfig(deps.DB, 1200)
	if cfg.StationCode != "KJFK" {
		t.Errorf("StationCode = %q, want %q (should be uppercased)", cfg.StationCode, "KJFK")
	}
}

func TestWeatherConfigStationNoValue(t *testing.T) {
	deps, mock := newTestDeps(t)

	handleWeatherConfig("/weather-config station", 1300, deps)

	if !containsStr(mock.lastMessage(), "Usage:") {
		t.Errorf("expected usage message, got: %s", mock.lastMessage())
	}
}

func TestWeatherConfigStationValidationFails(t *testing.T) {
	// Use a mock server that returns 404 (station not found)
	deps, mock := newTestDepsWithHTTPStatus(t, http.StatusNotFound)

	handleWeatherConfig("/weather-config station XYZZY", 1400, deps)

	// Should NOT have saved the station
	cfg, err := database.GetUserConfig(deps.DB, 1400)
	if err != nil {
		t.Fatalf("GetUserConfig failed: %v", err)
	}
	if cfg.StationCode != "KBWI" {
		t.Errorf("StationCode should remain default KBWI, got %q", cfg.StationCode)
	}

	// Should have sent an error message
	if !containsStr(mock.lastMessage(), "Invalid station code") {
		t.Errorf("expected 'Invalid station code' message, got: %s", mock.lastMessage())
	}
}

func TestWeatherConfigStationValidationSucceeds(t *testing.T) {
	// Use a mock server that returns 200 (valid station)
	deps, mock := newTestDepsWithHTTPStatus(t, http.StatusOK)

	handleWeatherConfig("/weather-config station KJFK", 1500, deps)

	cfg, err := database.GetUserConfig(deps.DB, 1500)
	if err != nil {
		t.Fatalf("GetUserConfig failed: %v", err)
	}
	if cfg.StationCode != "KJFK" {
		t.Errorf("StationCode = %q, want KJFK", cfg.StationCode)
	}

	if !containsStr(mock.lastMessage(), "KJFK") {
		t.Errorf("reply should mention KJFK, got: %s", mock.lastMessage())
	}
}

// --- Timezone-aware display tests ---

func TestRecurringWeatherShowsTimezone(t *testing.T) {
	deps, mock := newTestDeps(t)

	// Set user's timezone to PT (UTC-8)
	cfg := database.UserConfig{
		ChatID:         1600,
		StationCode:    "KBWI",
		City:           "Baltimore",
		State:          "MD",
		TimezoneName:   "PT",
		TimezoneOffset: -28800,
	}
	if err := database.UpsertUserConfig(deps.DB, cfg); err != nil {
		t.Fatal(err)
	}

	handleRecurringWeather("/recurring-weather 0 9 * * 1-5", 1600, deps)

	last := mock.lastMessage()
	if !containsStr(last, "PT") {
		t.Errorf("recurring weather confirmation should show user timezone 'PT', got: %s", last)
	}
	// Should NOT say "UTC"
	if containsStr(last, "UTC") {
		t.Errorf("recurring weather confirmation should not show 'UTC' when user has PT timezone, got: %s", last)
	}
}

func TestRecurringWeatherDefaultTimezone(t *testing.T) {
	deps, mock := newTestDeps(t)

	// No timezone configured -> should treat cron as UTC and show a notice
	handleRecurringWeather("/recurring-weather 0 9 * * *", 1700, deps)

	last := mock.lastMessage()
	if !containsStr(last, "UTC") {
		t.Errorf("recurring weather confirmation should show UTC when no timezone is set, got: %s", last)
	}
	if !containsStr(last, "timezone") {
		t.Errorf("recurring weather confirmation should show a timezone setup notice, got: %s", last)
	}
}

func TestWeatherScheduleShowsTimezone(t *testing.T) {
	deps, mock := newTestDeps(t)

	// Set user's timezone
	cfg := database.UserConfig{
		ChatID:         1800,
		StationCode:    "KBWI",
		City:           "Baltimore",
		State:          "MD",
		TimezoneName:   "CT",
		TimezoneOffset: -21600,
	}
	if err := database.UpsertUserConfig(deps.DB, cfg); err != nil {
		t.Fatal(err)
	}

	handleRecurringWeather("/recurring-weather 0 14 * * *", 1800, deps)
	handleWeatherSchedule(1800, deps)

	last := mock.lastMessage()
	if !containsStr(last, "CT") {
		t.Errorf("weather schedule should show user timezone 'CT', got: %s", last)
	}
}

func TestTimezoneUpdateReschedulesExistingSchedule(t *testing.T) {
	deps, mock := newTestDeps(t)

	const chatID = 1900

	// Create schedule WITHOUT timezone configured — stored UTC expr = original expr.
	handleRecurringWeather("/recurring-weather 0 9 * * 1-5", chatID, deps)

	sched, err := database.GetSchedule(deps.DB, chatID)
	if err != nil {
		t.Fatalf("schedule not created: %v", err)
	}
	if sched.CronExpressionUTC != "0 9 * * 1-5" {
		t.Fatalf("precondition: CronExpressionUTC = %q, want %q", sched.CronExpressionUTC, "0 9 * * 1-5")
	}

	// Now set timezone ET -5.
	handleWeatherConfig("/weather-config timezone ET -5", chatID, deps)

	last := mock.lastMessage()
	if !containsStr(last, "ET") {
		t.Errorf("timezone confirmation should mention ET, got: %s", last)
	}

	// The stored UTC expression should now be converted to 14:00 UTC (9am ET = 14:00 UTC).
	sched, err = database.GetSchedule(deps.DB, chatID)
	if err != nil {
		t.Fatalf("schedule missing after timezone update: %v", err)
	}
	if sched.CronExpressionUTC != "0 14 * * 1-5" {
		t.Errorf("CronExpressionUTC after timezone update = %q, want %q", sched.CronExpressionUTC, "0 14 * * 1-5")
	}

	// The confirmation message should include the updated next-run time in ET, not 4am.
	if !containsStr(last, "9:00 AM") {
		t.Errorf("timezone confirmation should show 9:00 AM ET next run, got: %s", last)
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
