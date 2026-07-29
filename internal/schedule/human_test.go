package schedule

import (
	"testing"
	"time"
)

func nextOf(t *testing.T, spec string) time.Time {
	t.Helper()
	p := NewAutoParser()
	s, err := p.Parse(spec)
	if err != nil {
		t.Fatalf("parse %q: %v", spec, err)
	}
	// reference: Wed 2026-01-07 08:00:00 local
	ref := time.Date(2026, 1, 7, 8, 0, 0, 0, time.UTC)
	return s.Next(ref)
}

func TestHumanForms(t *testing.T) {
	cases := []struct {
		spec string
		want string // RFC3339 of next fire after ref (Wed 2026-01-07 08:00 UTC)
	}{
		{"every 5 minutes", "2026-01-07T08:05:00Z"},
		{"every 2 hours", "2026-01-07T10:00:00Z"},
		{"every day at 9am", "2026-01-07T09:00:00Z"},
		{"every day at 21:00", "2026-01-07T21:00:00Z"},
		{"every monday at 9am", "2026-01-12T09:00:00Z"}, // next Monday
		{"every weekday at 8:30am", "2026-01-07T08:30:00Z"},
		{"every month on the 1st at midnight", "2026-02-01T00:00:00Z"},
		{"daily", "2026-01-08T00:00:00Z"},
		{"every mon,wed,fri at 6pm", "2026-01-07T18:00:00Z"}, // Wed today
		{"every day at noon", "2026-01-07T12:00:00Z"},
		// cron fallback still works:
		{"0 9 * * 1", "2026-01-12T09:00:00Z"},
		{"*/15 * * * *", "2026-01-07T08:15:00Z"},
	}
	for _, c := range cases {
		got := nextOf(t, c.spec).UTC().Format(time.RFC3339)
		if got != c.want {
			t.Errorf("%q: next = %s, want %s", c.spec, got, c.want)
		}
	}
}

func TestHumanErrorsAreHelpful(t *testing.T) {
	p := NewAutoParser()
	_, err := p.Parse("every blue moon")
	if err == nil {
		t.Fatal("expected error for nonsense phrase")
	}
	if !contains(err.Error(), "supported readable forms") {
		t.Errorf("error should list supported forms, got: %v", err)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
