package waitlist

import (
	"net/mail"
	"strings"
)

// ValidationError is shown to the visitor as-is, so its message follows the
// voice rules: say what is wrong and how to fix it.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

var (
	errEmpty    = &ValidationError{"Enter your email address."}
	errTooLong  = &ValidationError{"That email address is too long."}
	errNoAt     = &ValidationError{"That email address is missing an @, like sam@homelab.dev."}
	errNoLocal  = &ValidationError{"That email address is missing the part before the @."}
	errNoDomain = &ValidationError{"That email address is missing its domain, like .com or .dev."}
	errInvalid  = &ValidationError{"That doesn't look like an email address. Check it for typos."}
)

// NormalizeEmail checks an address and returns it lowercased, so the same
// person can't join twice by changing case. The web client mirrors these
// rules and messages in web/src/lib/waitlist.ts.
func NormalizeEmail(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return "", errEmpty
	case len(s) > 254:
		return "", errTooLong
	case !strings.Contains(s, "@"):
		return "", errNoAt
	}

	at := strings.LastIndex(s, "@")
	local, domain := s[:at], s[at+1:]
	if local == "" {
		return "", errNoLocal
	}
	dot := strings.LastIndex(domain, ".")
	if domain == "" || dot <= 0 || len(domain)-dot-1 < 2 {
		return "", errNoDomain
	}
	if strings.ContainsAny(s, " \t\r\n<>,;") {
		return "", errInvalid
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return "", errInvalid
	}
	return strings.ToLower(s), nil
}
