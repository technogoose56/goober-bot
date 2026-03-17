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
Bot:   Schedule set: "0 9 * * 1-5" (weekdays at 9:00 AM UTC).
       Next run: Mon Mar 18 09:00 AM.

User:  /recurring-weather 0 8 * * *
Bot:   Schedule updated: "0 8 * * *" (daily at 8:00 AM UTC).
       Next run: Tue Mar 18 08:00 AM.

User:  /weather-schedules
Bot:   Active schedule: "0 8 * * *"
       Created: 2026-03-17
       Last run: never

User:  /cancel-weather
Bot:   Schedule cancelled. You will no longer receive automatic weather updates.

User:  /cancel-weather
Bot:   No active schedule found for this chat.
```

---

## Implementation Status

Implemented on branch `implement-recurring-weather-plan-v2`. All phases
completed. All 32 tests pass (`go test ./...`). The project builds cleanly.

| Phase | Status | Notes |
|---|---|---|
| Phase 1 - Dependencies | Done | `modernc.org/sqlite` v1.47.0, `robfig/cron/v3` v3.0.1 |
| Phase 2 - Weather extraction | Done | `internal/weather/weather.go` with `FetchWeather`, `FormatWeather`, `SendWeather`, `SendWeatherToChat` |
| Phase 3 - Database layer | Done | `internal/database/db.go` with `Open`, `UpsertSchedule`, `RemoveSchedule`, `GetSchedule`, `ListSchedules`, `UpdateLastRun`. 10 tests in `db_test.go`. |
| Phase 4 - Scheduler | Done | `internal/scheduler/scheduler.go` with `New`, `Start`, `Stop`, `AddSchedule`, `RemoveSchedule`, `LoadFromDB`, `HasSchedule`, `Count`, `ValidateCron`, `NextRun`. 9 tests in `scheduler_test.go`. Mutex-protected, proper entryID tracking, DB rollback on failure. |
| Phase 5 - Command handlers | Done | `internal/commands/commands.go` with `HandleCommand` dispatch, `handleRecurringWeather`, `handleCancelWeather`, `handleWeatherSchedules`. 6 tests in `commands_test.go`. |
| Phase 6 - main.go integration | Done | Slim orchestration-only `main.go`. Fixed `/weather` length-check bug. |
| Phase 7 - Testing | Done | 32 tests across 4 packages. Database tests use `:memory:` SQLite. |
| Phase 8 - Documentation | Done | Updated `README.md` with new commands. Added `data/` to `.gitignore`. |

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

### What still needs improvement

- **End-to-end testing with a real Telegram bot:** The command handler tests
  use `recover()` to work around the nil bot. Introducing a `MessageSender`
  interface that wraps `bot.Send()` would allow proper mocking without panics.
- **Graceful shutdown:** The bot currently relies on the `for range updates`
  loop to block. Adding OS signal handling (`os.Signal` / `context.Context`)
  would ensure the scheduler and database are shut down cleanly on SIGINT/SIGTERM.
- **Rate limiting:** There is no guard against users setting cron expressions
  that fire very frequently (e.g., `* * * * *` = every minute). A minimum
  interval check (e.g., at least 15 minutes) would prevent abuse.
- **Timezone awareness:** Cron expressions run in UTC. Users may expect local
  time. Allowing a timezone argument or deriving it from the user's Telegram
  locale would improve UX.
- **Error resilience in scheduled sends:** If the NOAA API is down when a cron
  job fires, the error is logged but the user gets no notification. A retry
  mechanism or a "weather unavailable" message would be better.
- **Weather logs table:** The plan originally included a `weather_logs` table
  for audit purposes. This was intentionally deferred as it adds complexity
  without immediate user value.
- **Configurable weather station:** The station (KBWI/Baltimore) is hardcoded.
  A per-chat station configuration would make the bot useful beyond Baltimore.

---

## Future Enhancements (Out of Scope)

- Per-user weather station / location configuration.
- Timezone-aware cron expressions (display next-run in user's local time).
- Multiple schedules per chat.
- Admin command to list all schedules across all chats.
- Rate limiting to prevent cron expressions that fire too frequently.
- Webhook mode instead of long-polling for lower latency at scale.
