package access

import (
	"log"
	"strconv"
	"strings"
)

// Whitelist holds the set of allowed Telegram user IDs.
type Whitelist struct {
	allowed map[int64]struct{}
}

// Parse parses a comma-separated string of Telegram user IDs into a Whitelist.
// It trims whitespace around each entry and skips any entries that are not
// valid integers, logging a warning for each invalid entry. An empty input
// string produces an empty whitelist (open mode).
func Parse(csv string) *Whitelist {
	wl := &Whitelist{allowed: make(map[int64]struct{})}

	csv = strings.TrimSpace(csv)
	if csv == "" {
		return wl
	}

	for _, raw := range strings.Split(csv, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}

		id, err := strconv.ParseInt(entry, 10, 64)
		if err != nil {
			log.Printf("Whitelist: skipping invalid user ID %q: %v", entry, err)
			continue
		}

		wl.allowed[id] = struct{}{}
	}

	return wl
}

// IsAllowed reports whether userID is permitted to use the bot. If the
// whitelist is empty (open mode), every user is allowed. Otherwise only
// explicitly listed IDs are accepted.
func (w *Whitelist) IsAllowed(userID int64) bool {
	if len(w.allowed) == 0 {
		return true
	}
	_, ok := w.allowed[userID]
	return ok
}

// Count returns the number of user IDs in the whitelist.
func (w *Whitelist) Count() int {
	return len(w.allowed)
}
