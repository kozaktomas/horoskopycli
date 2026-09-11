package seznam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInDomain(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		candidate string
		want      bool
	}{
		"exact":              {candidate: "horoskopy.cz", want: true},
		"leading dot":        {candidate: ".horoskopy.cz", want: true},
		"subdomain":          {candidate: "api-web.horoskopy.cz", want: true},
		"dotted subdomain":   {candidate: ".www.horoskopy.cz", want: true},
		"lookalike suffix":   {candidate: "nothoroskopy.cz", want: false},
		"different site":     {candidate: "seznam.cz", want: false},
		"domain as a prefix": {candidate: "horoskopy.cz.evil.example", want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := inDomain(tt.candidate, cookieDomain); got != tt.want {
				t.Errorf("inDomain(%q) = %v, want %v", tt.candidate, got, tt.want)
			}
		})
	}
}

func TestSessionCookiePicksTheRightOne(t *testing.T) {
	t.Parallel()

	jar := []cookie{
		{Name: "sid", Value: "not this", Domain: ".horoskopy.cz"},
		{Name: CookieName, Value: "seznam only", Domain: ".seznam.cz"},
		{Name: CookieName, Value: "wanted", Domain: ".horoskopy.cz", Expires: 1820610121},
	}

	got, ok := sessionCookie(jar)
	if !ok {
		t.Fatal("sessionCookie did not find the session")
	}
	if got.Value != "wanted" {
		t.Errorf("sessionCookie = %q, want the horoskopy.cz one", got.Value)
	}
}

func TestSessionCookieIgnoresOtherDomains(t *testing.T) {
	t.Parallel()

	jar := []cookie{{Name: CookieName, Value: "seznam only", Domain: ".seznam.cz"}}

	if _, ok := sessionCookie(jar); ok {
		t.Error("sessionCookie should ignore a cookie the API would never receive")
	}
}

func TestSignedIn(t *testing.T) {
	t.Parallel()

	if signedIn([]cookie{{Name: "sid", Domain: ".seznam.cz"}}) {
		t.Error("signedIn should be false without a session cookie")
	}
	if !signedIn([]cookie{{Name: CookieName, Domain: ".seznam.cz"}}) {
		t.Error("signedIn should be true once Seznam has issued a session")
	}
}

// cookieReplies answers Storage.getCookies with a different jar on each call,
// repeating the last one, and counts Target.createTarget commands.
func cookieReplies(jars ...string) (reply func(map[string]any) string, polls, tabs *atomic.Int32) {
	var pollCount, tabCount atomic.Int32

	answerCommand := func(request map[string]any) string {
		id, ok := request["id"].(float64)
		if !ok {
			id = 0
		}

		if request["method"] == "Target.createTarget" {
			tabCount.Add(1)

			return string(mustJSON(map[string]any{"id": int(id), "result": map[string]any{"targetId": "abc"}}))
		}

		index := int(pollCount.Add(1)) - 1
		if index >= len(jars) {
			index = len(jars) - 1
		}

		return `{"id":` + jsonNumber(id) + `,"result":{"cookies":` + jars[index] + `}}`
	}

	return answerCommand, &pollCount, &tabCount
}

// mustJSON encodes a value for a canned browser reply.
func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return []byte(`{}`)
	}

	return encoded
}

// jsonNumber renders a command id the way it arrived.
func jsonNumber(id float64) string {
	return string(mustJSON(int(id)))
}

func TestWaitForSessionReturnsTheCookie(t *testing.T) {
	t.Parallel()

	reply, polls, tabs := cookieReplies(
		`[]`,
		`[{"name":"ds","value":"cookie-value","domain":".horoskopy.cz","expires":1820610121}]`,
	)
	conn, _ := newFakeBrowser(t, reply)

	session, err := waitForSession(context.Background(), conn, 5*time.Second, time.Millisecond, progress{})
	if err != nil {
		t.Fatalf("waitForSession returned %v", err)
	}
	if session.Cookie != "cookie-value" {
		t.Errorf("waitForSession cookie = %q, want the browser's", session.Cookie)
	}
	if session.Expires.Unix() != 1820610121 {
		t.Errorf("waitForSession expiry = %v, want the cookie's", session.Expires)
	}
	if polls.Load() < 2 {
		t.Errorf("waitForSession polled %d times, want it to keep waiting", polls.Load())
	}
	if tabs.Load() != 0 {
		t.Errorf("waitForSession opened %d tabs, want none while nobody is signed in", tabs.Load())
	}
}

func TestWaitForSessionHandsTheLoginOverOnce(t *testing.T) {
	t.Parallel()

	reply, _, tabs := cookieReplies(
		`[{"name":"ds","value":"seznam-only","domain":".seznam.cz"}]`,
		`[{"name":"ds","value":"seznam-only","domain":".seznam.cz"}]`,
		`[{"name":"ds","value":"seznam-only","domain":".seznam.cz"}]`,
		`[{"name":"ds","value":"cookie-value","domain":".horoskopy.cz"}]`,
	)
	conn, _ := newFakeBrowser(t, reply)

	session, err := waitForSession(context.Background(), conn, 5*time.Second, time.Millisecond, progress{})
	if err != nil {
		t.Fatalf("waitForSession returned %v", err)
	}
	if session.Cookie != "cookie-value" {
		t.Errorf("waitForSession cookie = %q, want the horoskopy.cz one", session.Cookie)
	}
	if tabs.Load() != 1 {
		t.Errorf("waitForSession opened %d tabs, want exactly one hand-over", tabs.Load())
	}
}

func TestWaitForSessionGivesUp(t *testing.T) {
	t.Parallel()

	reply, _, _ := cookieReplies(`[]`)
	conn, _ := newFakeBrowser(t, reply)

	_, err := waitForSession(context.Background(), conn, 20*time.Millisecond, time.Millisecond, progress{})
	if !errors.Is(err, ErrLoginTimeout) {
		t.Errorf("waitForSession = %v, want ErrLoginTimeout", err)
	}
}

func TestWaitForSessionStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	reply, _, _ := cookieReplies(`[]`)
	conn, _ := newFakeBrowser(t, reply)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForSession(ctx, conn, time.Minute, time.Millisecond, progress{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("waitForSession after cancel = %v, want context.Canceled", err)
	}
}

func TestSleepReturnsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sleep(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("sleep = %v, want context.Canceled", err)
	}
}

func TestSleepWaits(t *testing.T) {
	t.Parallel()

	started := time.Now()
	if err := sleep(context.Background(), 10*time.Millisecond); err != nil {
		t.Fatalf("sleep returned %v", err)
	}
	if elapsed := time.Since(started); elapsed < 10*time.Millisecond {
		t.Errorf("sleep returned after %s, want at least 10ms", elapsed)
	}
}

func TestLoginNeedsABrowser(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := Login(context.Background(), LoginOptions{Browser: "definitely-not-a-browser"})
	if err == nil {
		t.Error("Login without a usable browser should fail")
	}
}

func TestDescribeJar(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		jar  []cookie
		want string
	}{
		"nothing yet": {
			jar:  []cookie{{Name: "sid", Domain: ".seznam.cz"}},
			want: "1 cookies, no Seznam session yet",
		},
		"signed in to seznam only": {
			jar:  []cookie{{Name: CookieName, Domain: ".seznam.cz"}},
			want: "1 cookies, Seznam session on .seznam.cz",
		},
		"handed over": {
			jar: []cookie{
				{Name: CookieName, Domain: ".seznam.cz"},
				{Name: CookieName, Domain: ".horoskopy.cz"},
			},
			want: "2 cookies, Seznam session on .seznam.cz, .horoskopy.cz",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := describeJar(tt.jar); got != tt.want {
				t.Errorf("describeJar() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWaitForSessionReportsWhatItSees(t *testing.T) {
	t.Parallel()

	reply, _, _ := cookieReplies(
		`[{"name":"ds","value":"seznam-only","domain":".seznam.cz"}]`,
		`[{"name":"ds","value":"cookie-value","domain":".horoskopy.cz"}]`,
	)
	conn, _ := newFakeBrowser(t, reply)

	var out bytes.Buffer
	if _, err := waitForSession(context.Background(), conn, 5*time.Second, time.Millisecond,
		progress{out: &out, debug: true}); err != nil {
		t.Fatalf("waitForSession returned %v", err)
	}

	printed := out.String()
	for _, want := range []string{"Seznam session on .seznam.cz", "hand the session over"} {
		if !strings.Contains(printed, want) {
			t.Errorf("debug output %q should mention %q", printed, want)
		}
	}
}

func TestProgressStaysQuietWithoutAWriter(t *testing.T) {
	t.Parallel()

	// Neither call may panic: Progress is optional, and debug output is only
	// wanted when asked for.
	progress{}.printf("ignored %d\n", 1)
	progress{debug: true}.printf("ignored\n")

	var out bytes.Buffer
	progress{out: &out}.debugf("not debugging\n")
	if out.Len() != 0 {
		t.Errorf("debugf wrote %q without being asked to debug", out.String())
	}
}
