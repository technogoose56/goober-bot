package access

import "testing"

func TestParseEmpty(t *testing.T) {
	wl := Parse("")
	if wl.Count() != 0 {
		t.Errorf("expected count 0, got %d", wl.Count())
	}
	// Open mode: any ID should be allowed
	if !wl.IsAllowed(123) {
		t.Error("empty whitelist should allow any user (open mode)")
	}
}

func TestParseSingleID(t *testing.T) {
	wl := Parse("123")
	if wl.Count() != 1 {
		t.Errorf("expected count 1, got %d", wl.Count())
	}
	if !wl.IsAllowed(123) {
		t.Error("expected 123 to be allowed")
	}
	if wl.IsAllowed(456) {
		t.Error("expected 456 to be rejected")
	}
}

func TestParseMultipleIDs(t *testing.T) {
	wl := Parse("123,456,789")
	if wl.Count() != 3 {
		t.Errorf("expected count 3, got %d", wl.Count())
	}
	for _, id := range []int64{123, 456, 789} {
		if !wl.IsAllowed(id) {
			t.Errorf("expected %d to be allowed", id)
		}
	}
	if wl.IsAllowed(999) {
		t.Error("expected 999 to be rejected")
	}
}

func TestParseWhitespace(t *testing.T) {
	wl := Parse(" 123 , 456 ")
	if wl.Count() != 2 {
		t.Errorf("expected count 2, got %d", wl.Count())
	}
	if !wl.IsAllowed(123) {
		t.Error("expected 123 to be allowed after trimming")
	}
	if !wl.IsAllowed(456) {
		t.Error("expected 456 to be allowed after trimming")
	}
}

func TestParseInvalidEntries(t *testing.T) {
	wl := Parse("123,abc,456")
	if wl.Count() != 2 {
		t.Errorf("expected count 2, got %d", wl.Count())
	}
	if !wl.IsAllowed(123) {
		t.Error("expected 123 to be allowed")
	}
	if !wl.IsAllowed(456) {
		t.Error("expected 456 to be allowed")
	}
}

func TestParseAllInvalid(t *testing.T) {
	wl := Parse("abc,def")
	if wl.Count() != 0 {
		t.Errorf("expected count 0, got %d", wl.Count())
	}
	// All entries invalid -> empty whitelist -> open mode
	if !wl.IsAllowed(123) {
		t.Error("all-invalid whitelist should fall back to open mode")
	}
}

func TestIsAllowedOpenMode(t *testing.T) {
	wl := Parse("")
	ids := []int64{1, 100, 999999999, -1}
	for _, id := range ids {
		if !wl.IsAllowed(id) {
			t.Errorf("open mode should allow ID %d", id)
		}
	}
}

func TestIsAllowedRejectsUnlisted(t *testing.T) {
	wl := Parse("100,200")
	unlisted := []int64{1, 99, 101, 199, 201, 999}
	for _, id := range unlisted {
		if wl.IsAllowed(id) {
			t.Errorf("expected ID %d to be rejected", id)
		}
	}
}

func TestCount(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"1", 1},
		{"1,2,3", 3},
		{"1,abc,3", 2},
		{"abc", 0},
		{"1,1,1", 1}, // duplicates collapse
	}
	for _, tt := range tests {
		wl := Parse(tt.input)
		if got := wl.Count(); got != tt.want {
			t.Errorf("Parse(%q).Count() = %d, want %d", tt.input, got, tt.want)
		}
	}
}
