package jobs

import (
	"fmt"
	"strconv"
	"strings"
)

var dayNames = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
var monthNames = [13]string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

// Describe says when a schedule runs, in words, like "Every day at 09:00
// UTC" or "Every 15 minutes, Monday to Friday, in UTC". zone is an IANA
// name; empty is UTC.
func Describe(expr, zone string) (string, error) {
	s, err := Parse(expr)
	if err != nil {
		return "", err
	}
	return s.Describe(zone), nil
}

// Describe says when the schedule runs, in words.
func (s Schedule) Describe(zone string) string {
	where := "UTC"
	if zone != "" && zone != "UTC" {
		where = zone + " time"
	}
	f := s.fields
	mins, hours := values(s.minute, 0, 59), values(s.hour, 0, 23)
	minStep, hourStep := stepOf(f[0]), stepOf(f[1])

	// When in the day: either a few times ("09:00 and 17:00"), or how often.
	var times []string
	often := ""
	switch {
	case f[0] == "*" && f[1] == "*":
		often = "Every minute"
	case minStep > 0 && f[1] == "*":
		often = "Every " + plural(minStep, "minute")
	case f[1] == "*" && len(mins) <= 4:
		often = "Every hour, " + past(mins)
	case hourStep > 0 && len(mins) <= 4:
		often = "Every " + plural(hourStep, "hour") + ", " + past(mins)
	case len(mins) <= 4 && len(mins)*len(hours) <= 6:
		for _, h := range hours {
			for _, m := range mins {
				times = append(times, fmt.Sprintf("%02d:%02d", h, m))
			}
		}
	case (f[0] == "*" || minStep > 0) && contiguous(hours):
		often = "Every minute"
		if minStep > 0 {
			often = "Every " + plural(minStep, "minute")
		}
		often += fmt.Sprintf(" from %02d:00 to %02d:59 %s", hours[0], hours[len(hours)-1], where)
	default:
		often = "At " + plural0(len(mins), "minute") + " " + numbers(mins) + " past " + plural0(len(hours), "hour") + " " + numbers(hours) + " " + where
	}
	zoned := len(times) > 0 || strings.HasSuffix(often, where)

	// Which days, and which months.
	days, single := s.days()
	months := s.months()

	if len(times) > 0 {
		at := and(times) + " " + where
		var out string
		switch {
		case days == "" && months == "":
			out = "Every day at " + at
		case days == "":
			out = "Every day at " + at + ", " + months
		case single != "":
			out = "Every " + single + " at " + at
			if months != "" {
				out += ", " + months
			}
		default:
			out = "At " + at + days
			if months != "" {
				out += ", " + months
			}
		}
		return out
	}
	out := often
	if strings.HasPrefix(days, ",") {
		out += days
	} else if days != "" {
		out += "," + days
	}
	if months != "" {
		out += ", " + months
	}
	if !zoned && (days != "" || months != "" || f[1] != "*" && hourStep == 0) {
		out += ", in " + strings.TrimSuffix(where, " time")
	}
	return out
}

// days says which days a schedule runs on, after the times, like " on the
// 1st of every month" or ", Monday to Friday". single is the one weekday a
// schedule runs on, if that's all it says, for "Every Monday at 09:00".
func (s Schedule) days() (text, single string) {
	f := s.fields
	domAll, dowAll := f[2] == "*", f[4] == "*"
	if domAll && dowAll {
		return "", ""
	}
	weekdays := ""
	if !dowAll {
		dows := values(s.dow, 0, 6)
		switch {
		case len(dows) == 1 && domAll:
			return " on " + dayNames[dows[0]] + "s", dayNames[dows[0]]
		case len(dows) == 2 && dows[0] == 0 && dows[1] == 6:
			weekdays = "on Saturdays and Sundays"
		case len(dows) > 2 && contiguous(dows):
			weekdays = dayNames[dows[0]] + " to " + dayNames[dows[len(dows)-1]]
		default:
			var names []string
			for _, d := range dows {
				names = append(names, dayNames[d]+"s")
			}
			weekdays = "on " + and(names)
		}
	}
	monthDays := ""
	if !domAll {
		doms := values(s.dom, 1, 31)
		of := " of the month"
		if f[3] == "*" {
			of = " of every month"
		}
		switch step := stepOf(f[2]); {
		case step > 0:
			monthDays = "every " + plural(step, "day") + " from the 1st" + of
		case len(doms) <= 4:
			var ords []string
			for _, d := range doms {
				ords = append(ords, ordinal(d))
			}
			monthDays = "on the " + and(ords) + of
		case contiguous(doms):
			monthDays = fmt.Sprintf("on days %d to %d%s", doms[0], doms[len(doms)-1], of)
		default:
			monthDays = "on days " + numbers(doms) + of
		}
	}
	switch {
	case weekdays != "" && monthDays != "":
		return " " + monthDays + ", and " + weekdays, ""
	case monthDays != "":
		return " " + monthDays, ""
	case strings.HasPrefix(weekdays, "on "):
		return " " + weekdays, ""
	}
	return ", " + weekdays, ""
}

// months says which months, like "in January" or "from March to June".
// Days of the month say "of the month" themselves.
func (s Schedule) months() string {
	if s.fields[3] == "*" {
		return ""
	}
	ms := values(s.month, 1, 12)
	if len(ms) > 2 && contiguous(ms) {
		return "from " + monthNames[ms[0]] + " to " + monthNames[ms[len(ms)-1]]
	}
	var names []string
	for _, m := range ms {
		names = append(names, monthNames[m])
	}
	return "in " + and(names)
}

func values(bits uint64, lo, hi int) []int {
	var out []int
	for v := lo; v <= hi; v++ {
		if has(bits, v) {
			out = append(out, v)
		}
	}
	return out
}

// stepOf is n for a field like */n, and 0 for anything else.
func stepOf(field string) int {
	if rest, ok := strings.CutPrefix(field, "*/"); ok {
		n, _ := strconv.Atoi(rest)
		return n
	}
	return 0
}

func contiguous(vs []int) bool {
	for i := 1; i < len(vs); i++ {
		if vs[i] != vs[i-1]+1 {
			return false
		}
	}
	return len(vs) > 1
}

// past says which minutes of the hour: "on the hour", "at 15 minutes past".
func past(mins []int) string {
	if len(mins) == 1 && mins[0] == 0 {
		return "on the hour"
	}
	var ns []string
	for _, m := range mins {
		ns = append(ns, strconv.Itoa(m))
	}
	if len(mins) == 1 && mins[0] == 1 {
		return "at 1 minute past"
	}
	return "at " + and(ns) + " minutes past"
}

func numbers(vs []int) string {
	if contiguous(vs) && len(vs) > 2 {
		return fmt.Sprintf("%d to %d", vs[0], vs[len(vs)-1])
	}
	var ns []string
	for _, v := range vs {
		ns = append(ns, strconv.Itoa(v))
	}
	return and(ns)
}

// and joins words as a sentence does: "a", "a and b", "a, b and c".
func and(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return strconv.Itoa(n) + suffix
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// plural0 is the word for a count, without the count: "minute", "minutes".
func plural0(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
