// Package jobs runs each project's scheduled jobs: a command in a fresh
// container from production's image, or a request to a path on production.
// Schedules are cron expressions, read in a time zone (UTC unless a job
// says otherwise). A run never starts while the job's last one is still
// going, and runs missed while pierd was down aren't made up.
package jobs

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression: minute, hour, day of the month,
// month and day of the week.
type Schedule struct {
	minute, hour, dom, month, dow uint64 // which values match, one bit each
	domAll, dowAll                bool   // the day fields were *, or a step over all of them
	fields                        [5]string
}

type field struct {
	name     string
	min, max int
	names    []string // names for the values from min, like JAN or SUN
}

var cronFields = [5]field{
	{name: "minute", min: 0, max: 59},
	{name: "hour", min: 0, max: 23},
	{name: "day of the month", min: 1, max: 31},
	{name: "month", min: 1, max: 12, names: []string{"JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"}},
	{name: "day of the week", min: 0, max: 7, names: []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}},
}

var macros = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *", "@weekly": "0 0 * * 0",
	"@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

// Parse reads a cron expression: five fields, each a *, a number, a range
// like 1-5, a list like 1,15, or any of those with a step like */10. Months
// and days of the week can be named (JAN, MON), and Sunday is 0 or 7. The
// macros @hourly, @daily, @weekly, @monthly and @yearly work too.
func Parse(expr string) (Schedule, error) {
	expr = strings.TrimSpace(expr)
	if m, ok := macros[strings.ToLower(expr)]; ok {
		expr = m
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, errors.New("A schedule has five parts: minute, hour, day of the month, month and day of the week, like 0 9 * * *.")
	}
	var s Schedule
	copy(s.fields[:], parts)
	sets := [5]*uint64{&s.minute, &s.hour, &s.dom, &s.month, &s.dow}
	for i, part := range parts {
		bits, all, err := parseField(part, cronFields[i])
		if err != nil {
			return Schedule{}, err
		}
		*sets[i] = bits
		switch i {
		case 2:
			s.domAll = all
		case 4:
			s.dowAll = all
		}
	}
	if s.dow&(1<<7) != 0 { // 7 is Sunday too
		s.dow |= 1
		s.dow &^= 1 << 7
	}
	return s, nil
}

func parseField(text string, f field) (bits uint64, all bool, err error) {
	for _, item := range strings.Split(text, ",") {
		if item == "" {
			return 0, false, fmt.Errorf("The %s has an empty item in its list: %q.", f.name, text)
		}
		rng, stepText, stepped := strings.Cut(item, "/")
		step := 1
		if stepped {
			step, err = strconv.Atoi(stepText)
			if err != nil || step < 1 || step > f.max {
				return 0, false, fmt.Errorf("The step in the %s must be a number from 1 to %d, not %q.", f.name, f.max, stepText)
			}
		}
		lo, hi := f.min, f.max
		if f.name == "day of the week" {
			hi = 6 // * means Sunday to Saturday once
		}
		switch {
		case rng == "*":
			// As in cron, a day field that starts with * doesn't restrict
			// the days, even with a step, so the other one alone decides.
			all = true
		default:
			a, b, isRange := strings.Cut(rng, "-")
			if lo, err = value(a, f); err != nil {
				return 0, false, err
			}
			hi = lo
			if stepped && !isRange {
				hi = f.max // 5/15 means from 5 to the end, every 15
			}
			if isRange {
				if hi, err = value(b, f); err != nil {
					return 0, false, err
				}
				if hi < lo {
					return 0, false, fmt.Errorf("The range %s in the %s runs backwards.", rng, f.name)
				}
			}
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << v
		}
	}
	return bits, all, nil
}

func value(text string, f field) (int, error) {
	for i, n := range f.names {
		if strings.EqualFold(text, n) {
			return f.min + i, nil
		}
	}
	v, err := strconv.Atoi(text)
	if err != nil || v < f.min || v > f.max {
		return 0, fmt.Errorf("The %s must be from %d to %d, not %q.", f.name, f.min, f.max, text)
	}
	return v, nil
}

func has(bits uint64, v int) bool { return bits&(1<<v) != 0 }

// Matches says whether the schedule runs in the minute t is in, read in
// t's own location. As in cron, when both day fields are set, either one
// matching is enough.
func (s Schedule) Matches(t time.Time) bool {
	if !has(s.minute, t.Minute()) || !has(s.hour, t.Hour()) || !has(s.month, int(t.Month())) {
		return false
	}
	return s.dayMatches(t)
}

func (s Schedule) dayMatches(t time.Time) bool {
	dom, dow := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	if s.domAll || s.dowAll {
		return dom && dow
	}
	return dom || dow
}

// Next is the first minute after t that the schedule runs, in loc. It's
// the zero time for a schedule that never runs, like 31 February.
func (s Schedule) Next(after time.Time, loc *time.Location) time.Time {
	t := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		y, mo, d := t.Date()
		switch {
		case !has(s.month, int(mo)):
			t = time.Date(y, mo+1, 1, 0, 0, 0, 0, loc)
		case !s.dayMatches(t):
			t = time.Date(y, mo, d+1, 0, 0, 0, 0, loc)
		case !has(s.hour, t.Hour()):
			t = time.Date(y, mo, d, t.Hour()+1, 0, 0, 0, loc)
		case !has(s.minute, t.Minute()):
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}
