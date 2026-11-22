package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"pier/internal/store"
)

const h = hiddenValue

// A viewer never sees a value, whatever its length: a long one is hidden
// wherever it appears, a short one wherever it stands as a whole word.
func TestHiderRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		in     string
		want   string
	}{
		{"a long value anywhere", []string{"sk_live_9f8e"}, "token=sk_live_9f8exyz", "token=" + h + "xyz"},
		{"a short value as a word", []string{"abc"}, "key=abc", "key=" + h},
		{"a short value alone", []string{"abc"}, "abc", h},
		{"a short value twice", []string{"abc"}, "abc abc", h + " " + h},
		{"not inside a longer word", []string{"abc"}, "abcdef xabc abc_ 1abc", "abcdef xabc abc_ 1abc"},
		{"next to punctuation", []string{"abc"}, "(abc), 'abc'.", "(" + h + "), '" + h + "'."},
		{"found after a near miss", []string{"abc"}, "xabc abc", "xabc " + h},
		{"edges with punctuation, whole", []string{"-x1"}, "flag -x1 on", "flag " + h + " on"},
		{"edges with punctuation, inside a word", []string{"-x1"}, "a-x1 -x1b", "a-x1 -x1b"},
		{"punctuation inside", []string{"a.b"}, "host=a.b. a.bc", "host=" + h + ". a.bc"},
		{"plain values left alone", []string{"true", "1", "on", "production", "", "No"}, "debug=true workers=1 on production no", "debug=true workers=1 on production no"},
		{"overlapping values hidden whole", []string{"abcdef", "sk_live_abcdef"}, "a sk_live_abcdef b abcdef", "a " + h + " b " + h},
		{"a short value inside a long one", []string{"abc", "abcdefgh"}, "abcdefgh abc", h + " " + h},
		{"a long value starting inside a short miss", []string{"key", "eyzzzz"}, "keyzzzz", "k" + h},
		{"a shorter value where a longer short one isn't whole", []string{"ab-", "ab"}, "ab-x", h + "-x"},
		{"each line of a value", []string{"line-one-secret\nxy"}, "line-one-secret then xy", h + " then " + h},
		{"non-ASCII letters are word characters", []string{"abc"}, "éabc abc", "éabc " + h},
		{"the stand-in never tells the length", []string{"ab", "a-much-longer-secret"}, "ab a-much-longer-secret", h + " " + h},
	} {
		if got := newHider(tc.values)(tc.in); got != tc.want {
			t.Errorf("%s: %q became %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// Members and the owner see every value as it is.
func TestOnlyViewersHaveValuesHidden(t *testing.T) {
	tm := newTeam(t, false)
	ctx := context.Background()
	tm.st.CreateProject(ctx, store.Project{Name: "blog"})
	tm.send(t, tm.member, "PUT", "/api/projects/blog/variables", `{"name":"PIN","value":"4821"}`)
	for _, who := range []string{store.RoleOwner, store.RoleMember, store.RoleViewer} {
		r := httptest.NewRequest("GET", "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), userKey, store.User{ID: 1, Role: who}))
		hide, err := tm.api.valueHider(r, "blog")
		if err != nil {
			t.Fatal(err)
		}
		got := hide("pin=4821")
		if hidden := strings.Contains(got, h); hidden != (who == store.RoleViewer) {
			t.Errorf("the %s read %q", who, got)
		}
	}
}
