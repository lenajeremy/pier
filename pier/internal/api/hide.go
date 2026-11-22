package api

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"pier/internal/store"
)

// hiddenValue stands in for a variable's value in a log a viewer reads. It's
// the same whatever the value, so it never tells a value's length.
const hiddenValue = "••••••"

// plainValues can't be secret, and hiding them would blank out every log:
// flags, and the words that name environments ("building for production").
var plainValues = map[string]bool{
	"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true, "0": true, "1": true,
	"production": true, "preview": true, "development": true, "staging": true,
}

// wholeWordUnder is the length, in characters, below which a value is only
// hidden as a whole word: "abc" hides in "key=abc", not in "abcdef".
const wholeWordUnder = 6

// valueHider hides a project's variable values, shared ones included, and
// the CRON_SECRET Pier makes for its jobs, in the logs and job output a
// viewer reads. Pier never writes a value to a log, but a build, an app or a
// job can print one. For members and the owner it changes nothing.
func (a *API) valueHider(r *http.Request, project string) (func(string) string, error) {
	keep := func(s string) string { return s }
	if userFrom(r.Context()).Role != store.RoleViewer {
		return keep, nil
	}
	all, err := a.Deployer.SecretValues(r.Context(), project)
	if err != nil {
		return nil, err
	}
	return newHider(all), nil
}

// newHider makes one matcher for every value, so a long log is read once
// whatever the number of values. A value of six characters or more is
// hidden wherever it appears; a shorter one only where it stands as a whole
// word, with no letter, digit or underscore right before or after it, even
// when the value itself starts or ends with punctuation. Where values
// overlap, the longest is hidden whole.
func newHider(values []string) func(string) string {
	seen := map[string]bool{}
	var list []string
	for _, value := range values {
		// A log line holds at most one line of a value.
		for _, line := range strings.Split(value, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || plainValues[strings.ToLower(line)] || seen[line] {
				continue
			}
			seen[line] = true
			list = append(list, line)
		}
	}
	if len(list) == 0 {
		return func(s string) string { return s }
	}
	// Longest first: the matcher takes the first alternative that matches
	// at a place, so a value that contains another is hidden whole.
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	quoted := make([]string, len(list))
	var short []string
	for i, v := range list {
		quoted[i] = regexp.QuoteMeta(v)
		if utf8.RuneCountInString(v) < wholeWordUnder {
			short = append(short, v)
		}
	}
	re := regexp.MustCompile(strings.Join(quoted, "|"))
	isShort := func(v string) bool { return utf8.RuneCountInString(v) < wholeWordUnder }

	return func(s string) string {
		var b strings.Builder
		last, pos := 0, 0
		for pos <= len(s) {
			loc := re.FindStringIndex(s[pos:])
			if loc == nil {
				break
			}
			start, end := pos+loc[0], pos+loc[1]
			if isShort(s[start:end]) && !wholeWord(s, start, end) {
				// A shorter value may still stand whole here, like "ab" in
				// "ab-x" when "ab-" doesn't.
				end = -1
				for _, v := range short {
					if len(v) < loc[1]-loc[0] && strings.HasPrefix(s[start:], v) && wholeWord(s, start, start+len(v)) {
						end = start + len(v)
						break
					}
				}
				if end < 0 {
					// Not here: look again from the next character, so a
					// longer value starting inside this one is still found.
					_, size := utf8.DecodeRuneInString(s[start:])
					pos = start + max(size, 1)
					continue
				}
			}
			b.WriteString(s[last:start])
			b.WriteString(hiddenValue)
			last, pos = end, end
		}
		if last == 0 {
			return s
		}
		b.WriteString(s[last:])
		return b.String()
	}
}

// wholeWord says whether s[start:end] has no word character, a letter, digit
// or underscore, right before or right after it.
func wholeWord(s string, start, end int) bool {
	if start > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:start]); wordRune(r) {
			return false
		}
	}
	if end < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[end:]); wordRune(r) {
			return false
		}
	}
	return true
}

func wordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
