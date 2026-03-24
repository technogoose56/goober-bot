# Changelog

All notable changes to goober-bot are documented here.

This project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased]

---

## [0.1.0] - 2026-03-24

Initial versioned release. Covers all work done prior to formal versioning.

### Added

**Weather**
- `/weather` command fetching live conditions from the NOAA Weather API
  (`api.weather.gov`) for a configurable station
- Temperature displayed in both °F and °C; reports wind speed, humidity,
  visibility, and description
- Per-chat weather config stored in SQLite (`/weather-config`):
  - `station <code>` — NOAA station code (e.g. `KJFK`), validated against the
    NOAA API before saving
  - `city <name>` — display city name
  - `state <abbrev>` — display state abbreviation
  - `timezone <name> <offset>` — local timezone label and UTC offset in hours
    (e.g. `ET -5`)
- Defaults: station `KBWI`, city Baltimore, state MD, timezone ET (UTC−4)

**Recurring weather scheduling**
- `/recurring-weather <cron>` — set an automatic weather update on a 5-field
  cron schedule; times are interpreted in the user's configured timezone and
  converted to UTC internally
- `/cancel-weather` — cancel the active schedule
- `/weather-schedule` — show the active schedule, created time, last run, and
  next run (all times displayed in the user's configured timezone)
- Minimum schedule interval enforced at 15 minutes
- Schedules persisted in SQLite and restored automatically on bot restart
- If no timezone is configured, the cron expression is treated as UTC and the
  user is shown a notice to set their timezone

**User access control**
- `ALLOWED_USER_IDS` environment variable — comma-separated Telegram user IDs;
  when set, the bot ignores messages from any user not on the list
- When unset, all users are allowed (open access)

**Infrastructure**
- SQLite database (`./data/schedules.db`) for schedule and config persistence;
  directory created automatically if absent
- Graceful shutdown on `SIGINT` / `SIGTERM`: drains in-flight cron jobs before
  exit
- Dockerfile for containerised deployment
- Version constant logged at startup

**Other commands**
- `/hi` — simple greeting response

### Technical

- Packages: `internal/weather`, `internal/database`, `internal/scheduler`,
  `internal/commands`, `internal/access`
- Test coverage across all packages (80+ tests)
- `MessageSender` interface on `Deps` enables unit testing without a live bot
- NOAA station validation via `httptest` server in tests

[Unreleased]: https://github.com/user/goober-bot/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/user/goober-bot/releases/tag/v0.1.0
