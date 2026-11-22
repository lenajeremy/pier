package jobs

import (
	"strings"
	"testing"
	"time"
)

func at(t *testing.T, s string, loc *time.Location) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Each expression is read the way cron reads it, and its next run is the
// first minute after now that it matches.
func TestParseAndNext(t *testing.T) {
	now := at(t, "2026-11-22 10:07", time.UTC) // a Sunday
	for expr, want := range map[string]string{
		"* * * * *":       "2026-11-22 10:08",
		"*/15 * * * *":    "2026-11-22 10:15",
		"0 9 * * *":       "2026-11-23 09:00",
		"30 9 * * 1-5":    "2026-11-23 09:30",
		"0 0 1 * *":       "2026-12-01 00:00",
		"0 9 * * MON":     "2026-11-23 09:00",
		"0 9 * * 7":       "2026-11-29 09:00", // 7 is Sunday
		"0 12 * jan *":    "2027-01-01 12:00",
		"5 */6 * * *":     "2026-11-22 12:05",
		"0 9,17 * * *":    "2026-11-22 17:00",
		"@hourly":         "2026-11-22 11:00",
		"@weekly":         "2026-11-29 00:00",
		"@yearly":         "2027-01-01 00:00",
		"0 0 13 * 5":      "2026-11-27 00:00", // the 13th, or any Friday: whichever comes first
		"0 0 */10 * *":    "2026-12-01 00:00", // days 1, 11, 21 and 31
		"0 0 29 2 *":      "2028-02-29 00:00",
		"10-12/2 3 * * *": "2026-11-23 03:10",
	} {
		s, err := Parse(expr)
		if err != nil {
			t.Errorf("%q: %v", expr, err)
			continue
		}
		next := s.Next(now, time.UTC)
		if got := next.Format("2006-01-02 15:04"); got != want {
			t.Errorf("Next(%q) = %s, want %s", expr, got, want)
		}
		if !s.Matches(next) {
			t.Errorf("%q doesn't match its own next run %s", expr, next)
		}
	}
	never, _ := Parse("0 0 31 2 *")
	if !never.Next(now, time.UTC).IsZero() {
		t.Error("31 February has a next run")
	}

	// A schedule in another zone runs at that zone's wall-clock time.
	london, _ := time.LoadLocation("Europe/London")
	nine, _ := Parse("0 9 * * *")
	summer := at(t, "2026-07-01 06:00", time.UTC)
	if got := nine.Next(summer, london).UTC().Format("15:04"); got != "08:00" {
		t.Errorf("09:00 in London in July is %s UTC, want 08:00", got)
	}
}

// A schedule that isn't one says why, in words.
func TestParseErrors(t *testing.T) {
	for expr, want := range map[string]string{
		"":              "five parts",
		"0 9 * *":       "five parts",
		"60 * * * *":    "The minute must be from 0 to 59, not \"60\".",
		"0 24 * * *":    "The hour must be from 0 to 23",
		"0 0 0 * *":     "The day of the month must be from 1 to 31",
		"0 0 * 13 *":    "The month must be from 1 to 12",
		"0 0 * * 8":     "The day of the week must be from 0 to 7",
		"*/0 * * * *":   "The step in the minute",
		"0 17-9 * * *":  "runs backwards",
		"0 9,,17 * * *": "empty item",
		"0 nine * * *":  "The hour must be",
	} {
		_, err := Parse(expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error with %q", expr, err, want)
		}
	}
}

// The preview under the schedule field says when it runs in words.
func TestDescribe(t *testing.T) {
	for _, c := range []struct{ expr, zone, want string }{
		{"* * * * *", "", "Every minute"},
		{"*/5 * * * *", "", "Every 5 minutes"},
		{"0 * * * *", "", "Every hour, on the hour"},
		{"15,45 * * * *", "", "Every hour, at 15 and 45 minutes past"},
		{"0 */6 * * *", "", "Every 6 hours, on the hour"},
		{"0 9 * * *", "", "Every day at 09:00 UTC"},
		{"0 9 * * *", "UTC", "Every day at 09:00 UTC"},
		{"0 9 * * *", "Europe/London", "Every day at 09:00 Europe/London time"},
		{"@daily", "", "Every day at 00:00 UTC"},
		{"0 9,17 * * *", "", "Every day at 09:00 and 17:00 UTC"},
		{"30 9 * * 1-5", "", "At 09:30 UTC, Monday to Friday"},
		{"0 9 * * 1", "", "Every Monday at 09:00 UTC"},
		{"0 10 * * 6,0", "", "At 10:00 UTC on Saturdays and Sundays"},
		{"0 8 * * 1,4", "", "At 08:00 UTC on Mondays and Thursdays"},
		{"0 0 1 * *", "", "At 00:00 UTC on the 1st of every month"},
		{"0 0 1,15 * *", "", "At 00:00 UTC on the 1st and 15th of every month"},
		{"0 12 * 1 *", "", "Every day at 12:00 UTC, in January"},
		{"0 3 1 1,7 *", "", "At 03:00 UTC on the 1st of the month, in January and July"},
		{"*/15 9-17 * * 1-5", "", "Every 15 minutes from 09:00 to 17:59 UTC, Monday to Friday"},
		{"*/10 * * * 1-5", "", "Every 10 minutes, Monday to Friday, in UTC"},
		{"0 0 */2 * *", "", "At 00:00 UTC every 2 days from the 1st of every month"},
	} {
		got, err := Describe(c.expr, c.zone)
		if err != nil || got != c.want {
			t.Errorf("Describe(%q, %q) = %q, %v; want %q", c.expr, c.zone, got, err, c.want)
		}
	}
}
