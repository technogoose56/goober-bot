# User Whitelist - Implementation Plan

## Goal

Restrict bot access so that only explicitly whitelisted Telegram users can
interact with it. All messages from non-whitelisted users are silently ignored.

---

## Current State

| Aspect | Detail |
|---|---|
| Access control | None. Any Telegram user can send commands and the bot will respond. |
| User identity | The bot only reads `update.Message.Chat.ID`. The `update.Message.From` field (which carries the sender's Telegram user ID) is never accessed. |
| Configuration | A single env var (`TELEGRAM_BOT_TOKEN`). No mechanism for runtime or startup configuration of allowed users. |
| Database tables | `schedules` and `user_config`, both keyed by `chat_id`. No user/access tables. |

### Key distinction: `Chat.ID` vs `From.ID`

In a **private** DM, `Chat.ID == From.ID` (the user's numeric Telegram ID).
In a **group chat**, `Chat.ID` is the group's ID; individual senders are
identified only via `From.ID`. The whitelist must check `From.ID` (the actual
user), not `Chat.ID` (the chat context).

---

## Design Decisions

### Whitelist source: environment variable

The whitelist is provided as a comma-separated list of Telegram user IDs in the
`ALLOWED_USER_IDS` environment variable:

```
ALLOWED_USER_IDS=123456789,987654321
```

Rationale:
- **Simple** -- no new DB tables, no admin commands, no migration.
- **Secure** -- the set of allowed users is controlled at deployment time, not
  by bot commands that could themselves be abused.
- **Docker-friendly** -- easily passed via `docker run -e` or a `.env` file.
- **Immutable at runtime** -- prevents privilege escalation through the bot
  itself.

A database-backed whitelist with admin commands (e.g., `/whitelist add <id>`)
is listed as a future enhancement but intentionally deferred. The env-var
approach is sufficient for a single-operator bot and avoids the complexity of
bootstrapping an initial admin.

### Enforcement point: `main.go` update loop

The check is placed at the **earliest possible point** in the message
processing pipeline -- immediately after extracting the message, before any
command parsing or handling. This ensures:

- Zero code paths can bypass the check.
- No unnecessary work (DB lookups, API calls) is done for unauthorized users.
- Command handlers and the database layer remain unchanged and untouched.

### Behavior for unauthorized users

Messages from non-whitelisted users are **silently ignored** (no reply, no
error). This is intentional:

- Avoids leaking information about the bot's existence or purpose.
- Prevents abuse where an attacker floods the bot to trigger error responses.
- Matches the behavior of most private Telegram bots.

### Empty whitelist = unrestricted access

If `ALLOWED_USER_IDS` is not set or is empty, the bot operates in open mode
(no restrictions). This preserves backward compatibility and avoids locking
operators out if they forget to set the variable.

---

## Architecture

The whitelist is implemented as a lightweight `access` package with a single
type and a check in `main.go`:

```
internal/
  access/
    access.go           Whitelist type, Parse(), IsAllowed()
    access_test.go      Unit tests
```

### Data flow

```
Update arrives
  -> main.go extracts update.Message.From.ID (user ID)
  -> whitelist.IsAllowed(userID) returns true/false
  -> if false: skip (continue), no reply
  -> if true:  proceed to command handling (existing flow unchanged)
```

---

## Step-by-Step Implementation

### Phase 1 - Access package (`internal/access/access.go`)

```go
package access

// Whitelist holds the set of allowed Telegram user IDs.
type Whitelist struct {
    allowed map[int64]struct{}
}
```

| Function | Purpose |
|---|---|
| `Parse(csv string) *Whitelist` | Parse a comma-separated string of user IDs. Trims whitespace, skips invalid entries, logs warnings. Returns a `Whitelist` (empty map if input is empty). |
| `(w *Whitelist) IsAllowed(userID int64) bool` | Returns `true` if the whitelist is empty (open mode) or if `userID` is in the allowed set. |
| `(w *Whitelist) Count() int` | Returns the number of whitelisted user IDs. |

Design notes:
- A `map[int64]struct{}` gives O(1) lookups.
- `Parse` is a pure function with no side effects beyond logging, making it
  easy to test.
- The "empty = unrestricted" semantic is in `IsAllowed`, not `Parse`, so the
  caller doesn't need conditional logic.

### Phase 2 - Integrate into `main.go`

Minimal changes to the update loop:

1. Read `ALLOWED_USER_IDS` from the environment at startup.
2. Call `access.Parse(...)` to build the whitelist.
3. Log the count of whitelisted users (or "unrestricted" if empty).
4. In the update loop, after the `update.Message == nil` check, extract
   `update.Message.From` and call `whitelist.IsAllowed(from.ID)`.
5. If not allowed, `continue` (skip the message entirely).

```go
// At startup:
wl := access.Parse(os.Getenv("ALLOWED_USER_IDS"))
if wl.Count() > 0 {
    log.Printf("Whitelist active: %d user(s) allowed", wl.Count())
} else {
    log.Println("Whitelist not configured: all users allowed")
}

// In update loop, after nil/empty checks:
from := update.Message.From
if from == nil || !wl.IsAllowed(from.ID) {
    continue
}
```

The `from == nil` guard handles the edge case of channel posts where `From` is
absent.

### Phase 3 - Testing (`internal/access/access_test.go`)

| Test | Behavior |
|---|---|
| `TestParseEmpty` | Empty string -> empty whitelist, `IsAllowed` returns true for any ID (open mode). |
| `TestParseSingleID` | `"123"` -> allows 123, rejects 456. |
| `TestParseMultipleIDs` | `"123,456,789"` -> allows all three, rejects others. |
| `TestParseWhitespace` | `" 123 , 456 "` -> trims correctly. |
| `TestParseInvalidEntries` | `"123,abc,456"` -> skips "abc", allows 123 and 456. |
| `TestParseAllInvalid` | `"abc,def"` -> empty whitelist (open mode). |
| `TestIsAllowedOpenMode` | Empty whitelist allows everyone. |
| `TestIsAllowedRejectsUnlisted` | Non-empty whitelist rejects unlisted IDs. |
| `TestCount` | Verify `Count()` returns correct number. |

### Phase 4 - Documentation

- Add `ALLOWED_USER_IDS` to the **Setup** section in `README.md`.
- Document the open-mode behavior (env var unset = no restrictions).
- Add an example for Docker usage with the env var.

---

## Files Changed Summary

| Action | File |
|---|---|
| Create | `internal/access/access.go` |
| Create | `internal/access/access_test.go` |
| Modify | `main.go` (add whitelist check in update loop) |
| Modify | `README.md` (document `ALLOWED_USER_IDS`) |

---

## User Experience

```
# Operator sets whitelist at deployment:
ALLOWED_USER_IDS=123456789,987654321

# Allowed user (ID 123456789) sends a message:
User:  /weather
Bot:   === Baltimore, MD Weather (KBWI Airport) ===
       ...

# Non-whitelisted user (ID 555555555) sends a message:
User:  /weather
Bot:   (no response)

# No whitelist configured (env var unset):
# All users can interact normally -- backward compatible.
```

---

## Implementation Status

Complete. All four phases implemented:
- Phase 1: `internal/access/access.go` with `Whitelist` type, `Parse()`, `IsAllowed()`, `Count()`
- Phase 2: `main.go` updated with whitelist initialization and enforcement in the update loop
- Phase 3: `internal/access/access_test.go` with 9 test functions covering all specified cases
- Phase 4: `README.md` updated with `ALLOWED_USER_IDS` documentation, Docker example, and project structure

---

## Future Enhancements (Out of Scope)

- **Database-backed whitelist with admin commands:** `/whitelist add <id>`,
  `/whitelist remove <id>`, `/whitelist list`. Requires bootstrapping an
  initial admin and adds significant complexity.
- **Group chat handling:** Allow whitelisting entire group chat IDs in addition
  to user IDs, so the bot can serve a group without listing every member.
- **Rejection message:** Optionally reply to non-whitelisted users with a
  "you are not authorized" message (configurable).
- **Rate limiting per user:** Pair the whitelist with per-user rate limits to
  prevent even authorized users from spamming.
