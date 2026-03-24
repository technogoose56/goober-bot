# Recurring Weather Updates - Implementation Plan

## Goal

Add a `/recurring-weather <cron expression>` command that lets users schedule
automatic weather updates. Schedules must survive bot restarts.

---

## Current State

| Aspect | Detail |
|---|---|
| Language | Go 1.26.1 |
| Source | Single `main.go` (140 lines) |
| Bot platform | Telegram via `go-telegram-bot-api/v5`, long-polling |
| Commands | `/weather` (Baltimore NOAA data), `/hi` + greetings |
| Persistence | None |
| Scheduling | None |
| Tests | None |
| Config | `TELEGRAM_BOT_TOKEN` env var; weather station hardcoded |

A previous attempt exists on `origin/implement-recurring-weather-plan`. That
branch has the right general idea but contains several bugs that must be fixed
in a fresh implementation (see [Prior Attempt Analysis](#prior-attempt-analysis)
below).

---

## Architecture

```
main.go                          Entry point: init DB, start scheduler, run update loop
internal/
  weather/weather.go             Weather fetch + format (extracted from main.go)
  database/db.go                 SQLite open, schema migration, CRUD for schedules
  database/schema.sql            SQL schema (reference / documentation)
  scheduler/scheduler.go         Cron lifecycle, per-chat job management
  commands/commands.go           Command parsing, dispatch, handler functions
```

### Data flow

```
User sends /recurring-weather 0 9 * * 1-5
  -> commands.go validates cron, calls scheduler.AddSchedule()
  -> scheduler stores entryID, calls database.UpsertSchedule()
  -> confirms to user

On bot start:
  -> database.ListSchedules() returns saved rows
  -> scheduler.AddSchedule() for each row (restores cron jobs)

Cron fires:
  -> scheduler callback calls weather.SendWeatherToChat(chatID, bot)
  -> database.UpdateLastRun(chatID)
```

---

## Step-by-Step Implementation

### Phase 1 - New dependencies

Add two new modules:

```sh
go get modernc.org/sqlite           # Pure-Go SQLite (no CGO needed)
go get github.com/robfig/cron/v3    # Cron expression parser + scheduler
```

`modernc.org/sqlite` is chosen over `mattn/go-sqlite3` because it requires no
C compiler, making the build fully portable.

### Phase 2 - Extract weather logic (`internal/weather/weather.go`)

Move `WeatherData`, `celsiusToFahrenheit`, and `sendWeather` out of `main.go`:

- `FetchWeather() (*WeatherData, error)` -- HTTP call + JSON decode.
- `FormatWeather(data *WeatherData) string` -- Build the message string.
- `SendWeather(chatID int64, bot *tgbotapi.BotAPI) *tgbotapi.MessageConfig` --
  For use in the `/weather` command handler (returns a message config).
- `SendWeatherToChat(chatID int64, bot *tgbotapi.BotAPI) error` -- For use by
  the scheduler (sends directly, returns error).

Keep the NOAA constants in this package for now. A future enhancement could make
the station configurable per user.

### Phase 3 - Database layer (`internal/database/`)

**Schema** (`schema.sql`, also embedded in `db.go` for auto-migration):

```sql
CREATE TABLE IF NOT EXISTS schedules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id         INTEGER NOT NULL UNIQUE,
    cron_expression TEXT    NOT NULL,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    last_run        TEXT
);
```

Design notes:
- `chat_id` is UNIQUE -- one schedule per chat. Users can update their
  schedule by running the command again.
- `cron_expression` should NOT have a UNIQUE constraint. Multiple chats may
  want the same schedule (e.g., `0 9 * * 1-5`). The prior attempt incorrectly
  made this UNIQUE.
- A `weather_logs` table is omitted for now. It adds complexity without clear
  user value and can be added later if audit logging is wanted.
- Timestamps use TEXT in ISO-8601 format (SQLite best practice).

**Functions** (`db.go`):

| Function | Purpose |
|---|---|
| `Open(path string) (*sql.DB, error)` | Open SQLite file, run schema migration, return handle |
| `Close(db *sql.DB)` | Close handle |
| `UpsertSchedule(db, chatID, cronExpr)` | INSERT OR REPLACE -- idempotent set/update |
| `RemoveSchedule(db, chatID)` | DELETE by chat_id, return error if not found |
| `GetSchedule(db, chatID) (Schedule, error)` | Single row lookup |
| `ListSchedules(db) ([]Schedule, error)` | All rows (for startup reload) |
| `UpdateLastRun(db, chatID, t time.Time)` | Stamp `last_run` after each send |

The `*sql.DB` handle is passed explicitly rather than stored in a package-level
global. This avoids hidden state and makes testing straightforward.

**Database file location:** `./data/schedules.db` (relative to working
directory). The `data/` directory is created automatically if missing. This
keeps the DB out of the source tree.

### Phase 4 - Scheduler (`internal/scheduler/scheduler.go`)

```go
type Scheduler struct {
    cron     *cron.Cron
    entries  map[int64]cron.EntryID   // chatID -> cron entry
    mu       sync.Mutex               // protects entries map
    db       *sql.DB
    sender   func(int64) error        // weather send callback
}
```

Key methods:

| Method | Behavior |
|---|---|
| `New(db, sender) *Scheduler` | Create cron instance (`cron.WithParser` for standard 5-field expressions) |
| `Start()` | `s.cron.Start()` |
| `Stop()` | `s.cron.Stop()` (context-aware) |
| `AddSchedule(chatID, cronExpr) error` | Validate expression, remove old entry if exists, add new entry, **store the returned `EntryID`**, persist to DB via `UpsertSchedule` |
| `RemoveSchedule(chatID) error` | Remove cron entry, delete from DB |
| `LoadFromDB() error` | Read all rows, call `addCronEntry` for each (no DB write) |

Critical fix vs. prior attempt: The `AddFunc` return value (`cron.EntryID`)
**must** be stored in `s.entries[chatID]`. The old code discarded it, making
`RemoveSchedule` and `GetSchedule` silently fail.

Concurrency: The `entries` map is protected by `sync.Mutex` since cron
callbacks run in separate goroutines.

Cron parser configuration: Use `cron.WithParser(cron.NewParser(cron.Minute |
cron.Hour | cron.Dom | cron.Month | cron.Dow))` for standard 5-field cron
expressions. The prior attempt used `cron.WithSeconds()` (6-field), which is
uncommon and confusing for users.

### Phase 5 - Command handlers (`internal/commands/commands.go`)

Three new commands:

#### `/recurring-weather <cron expression>`

1. Parse the message text: everything after `/recurring-weather ` is the cron
   expression (whitespace-joined).
2. Validate: attempt `cron.ParseStandard(expr)`. On failure, reply with a
   helpful error and an example.
3. Call `scheduler.AddSchedule(chatID, expr)`.
4. Reply with confirmation including the cron expression and when the next
   run will be.

Edge cases:
- No argument provided -> usage message with example.
- Invalid cron syntax -> specific parse error in reply.
- Command has `strings.HasPrefix` matching, not exact equality, so
  `/recurring-weather 0 9 * * 1-5` is matched correctly (the prior attempt
  only matched exact command names, ignoring arguments).

#### `/cancel-weather`

1. Call `scheduler.RemoveSchedule(chatID)`.
2. Reply with confirmation or "no schedule found" message.

The prior attempt did not actually call any removal logic -- it only sent a
hardcoded success message regardless of whether a schedule existed.

#### `/weather-schedules`

1. Call `database.GetSchedule(chatID)`.
2. Reply with cron expression, created date, and last run time (or "never").
3. If no schedule exists, reply with instructions to create one.

### Phase 6 - Update `main.go`

Slim `main.go` down to orchestration only:

```go
func main() {
    // 1. Read TELEGRAM_BOT_TOKEN
    // 2. Create bot API instance
    // 3. Open database  (database.Open("./data/schedules.db"))
    // 4. Create scheduler (scheduler.New(db, weatherSender))
    // 5. Load existing schedules (scheduler.LoadFromDB())
    // 6. Start scheduler
    // 7. defer scheduler.Stop(); defer database.Close(db)
    // 8. Enter update loop:
    //      - /weather       -> weather.SendWeather()
    //      - /recurring-*   -> commands.Handle()
    //      - /cancel-*      -> commands.Handle()
    //      - /weather-*     -> commands.Handle()
    //      - greetings      -> reply
}
```

Fix the existing `/weather` check: replace `len(text) > 7 && text ==
"/weather"` with a simple `text == "/weather"` (the length guard is
redundant and misleading).

### Phase 7 - Testing

| Area | Tests |
|---|---|
| `database/` | `TestUpsertSchedule`, `TestRemoveSchedule`, `TestListSchedules`, `TestGetSchedule` -- use an in-memory SQLite DB (`:memory:`) |
| `scheduler/` | `TestAddAndRemoveSchedule`, `TestLoadFromDB`, `TestInvalidCron` -- mock the sender func |
| `commands/` | `TestParseRecurringWeather`, `TestCancelWeatherNoSchedule` -- mock bot + scheduler |
| `weather/` | `TestFormatWeather`, `TestCelsiusToFahrenheit` -- unit tests on pure functions |

Run: `go test ./...`

### Phase 8 - Documentation

- Update `README.md` with new commands and usage examples.
- Add `data/` to `.gitignore` so the SQLite file is not committed.

---

## Prior Attempt Analysis

The `origin/implement-recurring-weather-plan` branch attempted this feature but
has the following bugs that must be avoided:

| # | File | Bug | Fix |
|---|---|---|---|
| 1 | `scheduler.go` | `AddSchedule` never stores the `cron.EntryID` returned by `AddFunc` in `s.handlers[chatID]`. All subsequent lookups/removals silently fail. | Store `entryID, err := s.cron.AddFunc(...)` then `s.entries[chatID] = entryID`. |
| 2 | `scheduler.go` | Validation creates a throwaway cron entry then removes `s.cron.Entries()[0].ID` -- this removes an arbitrary entry, not necessarily the one just added. | Validate with `cron.ParseStandard(expr)` instead of adding/removing a dummy entry. |
| 3 | `commands.go` | `ProcessCommand` compares `text == cmd.Name` exactly, so `/recurring-weather 0 9 * * 1-5` never matches `/recurring-weather`. | Use `strings.HasPrefix(text, cmd.Name)`. |
| 4 | `commands.go` | `handleRecurringWeather` calls `sender(chatID)` (sends weather immediately) but never actually registers the schedule in the scheduler or database. The cron expression is parsed but discarded. | Pass the scheduler and DB into the handler; call `scheduler.AddSchedule`. |
| 5 | `commands.go` | `handleCancelWeather` sends a success message but never calls any removal logic. | Call `scheduler.RemoveSchedule(chatID)`. |
| 6 | `db.go` | `cron_expression` column has a `UNIQUE` constraint, preventing two chats from having the same schedule. | Remove the UNIQUE constraint from `cron_expression`. |
| 7 | `db.go` | `isUniqueConstraintError` always returns `true` (just checks `err != nil`). | Check for SQLite error code 2067 (UNIQUE constraint) or match the error string. |
| 8 | `db.go` | Global `var DB *sql.DB` -- hidden mutable state. | Pass `*sql.DB` as a parameter. |
| 9 | `db.go` | `MkdirAll("internal/database", ...)` creates the directory inside the source tree. | Use a `data/` directory outside the source tree. |
| 10 | `scheduler.go` | Uses `cron.WithSeconds()` (6-field), but user-facing docs show 5-field expressions. | Use standard 5-field parser. |
| 11 | `main.go` | `/weather` guard `len(text) > 7 && text == "/weather"` is redundant and misleading. | Use `text == "/weather"`. |

---

## Files Changed Summary

| Action | File |
|---|---|
| Create | `internal/weather/weather.go` |
| Create | `internal/database/db.go` |
| Create | `internal/database/db_test.go` |
| Create | `internal/database/schema.sql` |
| Create | `internal/scheduler/scheduler.go` |
| Create | `internal/scheduler/scheduler_test.go` |
| Create | `internal/commands/commands.go` |
| Create | `internal/commands/commands_test.go` |
| Create | `internal/weather/weather_test.go` |
| Modify | `main.go` (slim down to orchestration) |
| Modify | `go.mod` (add `modernc.org/sqlite`, `robfig/cron/v3`) |
| Modify | `README.md` (document new commands) |
| Modify | `.gitignore` (add `data/`) |

---

## User Experience

```
User:  /recurring-weather 0 9 * * 1-5
Bot:   Schedule set: "0 9 * * 1-5"
       Next run: Mon Mar 18 09:00 AM UTC

User:  /recurring-weather 0 8 * * *
Bot:   Schedule set: "0 8 * * *"
       Next run: Tue Mar 18 08:00 AM UTC

User:  /recurring-weather * * * * *
Bot:   Invalid cron expression: schedule fires too frequently (every 1m0s);
       minimum interval is 15m0s

User:  /weather-schedule
Bot:   Active schedule: "0 8 * * *"
       Created: 2026-03-17
       Last run: never
       Next run: Tue Mar 18 08:00 AM UTC

User:  /cancel-weather
Bot:   Schedule cancelled. You will no longer receive automatic weather updates.

User:  /cancel-weather
Bot:   No active schedule found for this chat.

User:  /weather-config
Bot:   Current weather config:
         Station:  KBWI
         City:     Baltimore
         State:    MD
         Timezone: ET (UTC-4)

User:  /weather-config station KJFK
Bot:   Station set to "KJFK".

User:  /weather-config city New York
Bot:   City set to "New York".

User:  /weather-config timezone ET -5
Bot:   Timezone set to ET (UTC-5).
```

---

## Implementation Status

Implemented on branch `implement-recurring-weather-plan-v2`. All 71 tests pass
(`go test ./...`). The project builds cleanly with zero `go vet` warnings.

### Session 1 -- Core recurring weather feature

| Phase | Status | Notes |
|---|---|---|
| Phase 1 - Dependencies | Done | `modernc.org/sqlite` v1.47.0, `robfig/cron/v3` v3.0.1 |
| Phase 2 - Weather extraction | Done | `internal/weather/weather.go` with `FetchWeather`, `FormatWeather`, `SendWeather`, `SendWeatherToChat` |
| Phase 3 - Database layer | Done | `internal/database/db.go` with `Open`, `UpsertSchedule`, `RemoveSchedule`, `GetSchedule`, `ListSchedules`, `UpdateLastRun`. 10 tests. |
| Phase 4 - Scheduler | Done | `internal/scheduler/scheduler.go` with `New`, `Start`, `Stop`, `AddSchedule`, `RemoveSchedule`, `LoadFromDB`, `HasSchedule`, `Count`, `ValidateCron`, `NextRun`. 9 tests. Mutex-protected, proper entryID tracking, DB rollback on failure. |
| Phase 5 - Command handlers | Done | `internal/commands/commands.go` with `HandleCommand` dispatch, `handleRecurringWeather`, `handleCancelWeather`, `handleWeatherSchedule`. 6 tests. |
| Phase 6 - main.go integration | Done | Slim orchestration-only `main.go`. Fixed `/weather` length-check bug. |
| Phase 7 - Testing | Done | 32 tests across 4 packages. |
| Phase 8 - Documentation | Done | Updated `README.md` with new commands. Added `data/` to `.gitignore`. |

### Session 2 -- Improvements and new features

| Change | Status | Notes |
|---|---|---|
| Rename `/weather-schedules` to `/weather-schedule` | Done | Single schedule per chat, so singular name is correct. Updated commands, tests, README. |
| Rate limiting (15 min minimum) | Done | `ValidateCron` now samples 5 consecutive firings and rejects if any gap < 15 minutes. 6 new rate-limit test cases. |
| Per-user config (`/weather-config`) | Done | New `user_config` DB table. `GetUserConfig` returns defaults for unconfigured chats. Settings: `station`, `city`, `state`, `timezone`. 4 new DB tests, 10 new command tests. |
| Weather package refactored for config | Done | `FetchWeather(cfg)` and `FormatWeather(data, cfg)` accept a `Config` struct. Constants removed; station/city/state/timezone are now per-user. 4 new weather tests. |
| Scheduler uses per-chat config | Done | `sender` callback in `main.go` looks up `user_config` from DB before fetching weather. |
| `MessageSender` interface | Done | `commands.Deps.Bot` is now a `MessageSender` interface instead of `*tgbotapi.BotAPI`. Tests use `mockSender` -- no more `recover()` hacks. All 22 command tests use proper mocking. |
| Graceful shutdown | Done | `main.go` listens for SIGINT/SIGTERM via `os/signal`, calls `bot.StopReceivingUpdates()`, deferred `sched.Stop()` and `database.Close()` run cleanly. |

### All 11 prior-attempt bugs fixed

1. EntryID now stored in `s.entries[chatID]` after `AddFunc`.
2. Validation uses `cronParser.Parse(expr)` -- no throwaway entries.
3. Command matching uses `strings.HasPrefix`, not exact equality.
4. `handleRecurringWeather` calls `scheduler.AddSchedule` which persists to DB.
5. `handleCancelWeather` calls `scheduler.RemoveSchedule`.
6. `cron_expression` column has no UNIQUE constraint.
7. `isUniqueConstraintError` removed; upsert via `ON CONFLICT` instead.
8. `*sql.DB` passed explicitly -- no package-level global.
9. Database stored in `./data/schedules.db`, outside source tree.
10. Standard 5-field cron parser (Minute|Hour|Dom|Month|Dow).
11. `/weather` uses simple `text == "/weather"`.

### Session 3 -- Station validation and timezone-aware display

| Change | Status | Notes |
|---|---|---|
| NOAA station validation | Done | `weather.ValidateStation(code, client)` queries `api.weather.gov/stations/{CODE}` to verify the station exists before saving. Returns descriptive errors for 404 (not found) and other status codes. `Deps.HTTPClient` field added for testability. |
| Station validation in `/weather-config station` | Done | Command handler now calls `ValidateStation` before persisting. Invalid stations are rejected with a user-friendly error. |
| Station validation tests | Done | 5 new weather tests (`TestStationEndpoint`, `TestValidateStationValid`, `TestValidateStationNotFound`, `TestValidateStationServerError`, `TestValidateStationNilClient`) using `httptest.Server` with `rewriteTransport`. 2 new command tests (`TestWeatherConfigStationValidationFails`, `TestWeatherConfigStationValidationSucceeds`). |
| Timezone-aware cron display | Done | `scheduler.FormatNextRun(expr, tzName, tzOffsetSecs)` and `scheduler.FormatTimeInTZ(timeStr, tzName, tzOffsetSecs)` format times in the user's configured timezone. |
| Timezone-aware `/recurring-weather` confirmation | Done | Next-run time now displays in user's timezone (e.g., "Wed Mar 18 5:00 AM ET") instead of UTC. |
| Timezone-aware `/weather-schedule` display | Done | Both next-run and last-run times display in user's timezone. Falls back gracefully if time cannot be parsed. |
| Timezone display tests | Done | 7 new scheduler tests (`TestFormatNextRunDefaultTimezone`, `TestFormatNextRunPacificTimezone`, `TestFormatNextRunInvalidCron`, `TestFormatTimeInTZRFC3339`, `TestFormatTimeInTZPacific`, `TestFormatTimeInTZUnparseable`, `TestFormatTimeInTZNever`). 3 new command tests (`TestRecurringWeatherShowsTimezone`, `TestRecurringWeatherDefaultTimezone`, `TestWeatherScheduleShowsTimezone`). |

### Test summary (71 tests)

| Package | Tests | Coverage area |
|---|---|---|
| `internal/database` | 14 | Schema creation, schedule CRUD, upsert semantics, user config CRUD, defaults |
| `internal/scheduler` | 20 | Cron validation, rate limiting, add/remove/replace schedules, DB loading, entry registration, timezone-aware formatting (`FormatNextRun`, `FormatTimeInTZ`), edge cases (invalid cron, unparseable time) |
| `internal/commands` | 25 | Command routing (14 cases), recurring weather parsing, cron validation errors, rate limit rejection, cancel integration, schedule display, config set/get (station, city, state, timezone), multi-setting persistence, unknown setting, input normalization, station validation (valid/invalid), timezone display in recurring-weather and weather-schedule |
| `internal/weather` | 12 | Celsius conversion, format with default/custom config, zero values, LocationName, APIEndpoint, DefaultConfig, StationEndpoint, ValidateStation (valid/not-found/server-error/nil-client) |

### Session 4 -- Timezone-aware scheduling

| Change | Status | Notes |
|---|---|---|
| `cron_expression_utc` DB column | Done | Added via migration in `Open()`. Existing rows are backfilled with their existing expression (treated as UTC). New rows always store both the user expression and the UTC expression. |
| `UpsertSchedule` takes both expressions | Done | `UpsertSchedule(db, chatID, userExpr, utcExpr string)` — stores user's original expression for display and the UTC-converted expression for scheduling. |
| `HasUserConfig(db, chatID) (bool, error)` | Done | Returns true if the chat has an explicit row in `user_config`. Used to distinguish "no timezone configured" from "using default timezone". |
| `ConvertCronToUTC(expr, offsetSecs)` | Done | Converts simple cron expressions (single values, comma-separated lists, ranges in the hour field) from a local timezone to UTC. Handles midnight-boundary day-of-week shifts. Passes through step-based and wildcard-hour expressions unchanged. |
| `AddSchedule` uses UTC expression | Done | Schedules the UTC expression with the cron runner; stores both via `UpsertSchedule`. |
| `LoadFromDB` uses `CronExpressionUTC` | Done | Bot restarts correctly reload the pre-converted UTC expression without re-converting. |
| No timezone configured → UTC + notice | Done | If no `user_config` row exists, the cron expression is treated as UTC and the user receives a notice to set their timezone via `/weather-config timezone`. |
| Next-run display uses UTC expression | Done | `FormatNextRun` is called with the UTC expression and the user's timezone to correctly show the local equivalent time. |
| Plan updated | Done | This section. |

### What still needs improvement

- **Error resilience in scheduled sends:** If the NOAA API is down when a cron
  job fires, the error is logged but the user gets no notification. A retry
  mechanism or a "weather unavailable" message would be better.
- **Weather logs table:** The plan originally included a `weather_logs` table
  for audit purposes. This was intentionally deferred as it adds complexity
  without immediate user value.

---

## Future Enhancements (Out of Scope)

- Admin command to list all schedules across all chats.
- Webhook mode instead of long-polling for lower latency at scale.
- `/weather-config reset` command to restore defaults.
