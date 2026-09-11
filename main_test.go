package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kozaktomas/horoskopycli/internal/horoskopy"
	"github.com/kozaktomas/horoskopycli/internal/seznam"
)

// fakeFetcher stands in for the horoskopy.cz client so the CLI can be exercised
// without touching the network.
type fakeFetcher struct {
	horoscope horoskopy.Horoscope
	err       error

	gotSign   horoskopy.Sign
	gotPeriod horoskopy.Period
	calls     int
}

// Fetch records the arguments it was called with and returns the canned result.
func (f *fakeFetcher) Fetch(_ context.Context, sign horoskopy.Sign, period horoskopy.Period) (horoskopy.Horoscope, error) {
	f.calls++
	f.gotSign = sign
	f.gotPeriod = period

	return f.horoscope, f.err
}

// newFakeFetcher returns a fetcher answering with a minimal valid horoscope.
func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		horoscope: horoskopy.Horoscope{
			Title:    "Býk dnes",
			Sections: []horoskopy.Section{{Text: "Dnes to bude dobré."}},
		},
	}
}

// fakeInterpreter stands in for the dream book.
type fakeInterpreter struct {
	interpretation string
	err            error

	gotCookie string
	gotDream  string
	calls     int
}

// InterpretDream records what it was asked and returns the canned answer.
func (f *fakeInterpreter) InterpretDream(_ context.Context, cookie, dream string) (string, error) {
	f.calls++
	f.gotCookie = cookie
	f.gotDream = dream

	return f.interpretation, f.err
}

// fakeStore stands in for the session file.
type fakeStore struct {
	session seznam.Session
	loadErr error

	saved  *seznam.Session
	forgot bool
}

// Load returns the canned session.
func (f *fakeStore) Load() (seznam.Session, error) {
	return f.session, f.loadErr
}

// Save records the session that would have been written.
func (f *fakeStore) Save(session seznam.Session) error {
	f.saved = &session

	return nil
}

// Forget records that the session would have been removed.
func (f *fakeStore) Forget() error {
	f.forgot = true

	return nil
}

// Path returns a fixed location, so messages can be asserted on.
func (f *fakeStore) Path() (string, error) {
	return "/tmp/horoskopycli/session.json", nil
}

// fakeAccounts stands in for the Seznam login service.
type fakeAccounts struct {
	account    string
	accountErr error
	revokeErr  error

	revoked string
}

// Account returns the canned account name.
func (f *fakeAccounts) Account(_ context.Context, _ string) (string, error) {
	return f.account, f.accountErr
}

// Revoke records the cookie it was asked to invalidate.
func (f *fakeAccounts) Revoke(_ context.Context, cookie string) error {
	f.revoked = cookie

	return f.revokeErr
}

// newTestApp assembles an app with fakes and a buffer to write to.
func newTestApp(t *testing.T) (*app, *bytes.Buffer) {
	t.Helper()

	var out bytes.Buffer
	cli := &app{
		horoscopes: newFakeFetcher(),
		dreams:     &fakeInterpreter{interpretation: "Ryba znamená intuici."},
		sessions:   &fakeStore{session: seznam.Session{Cookie: "cookie-value"}},
		accounts:   &fakeAccounts{account: "panbotka@seznam.cz"},
		browserLogin: func(_ context.Context, _ seznam.LoginOptions) (seznam.Session, error) {
			return seznam.Session{Cookie: "browser-cookie", Expires: time.Date(2027, 9, 10, 0, 0, 0, 0, time.UTC)}, nil
		},
		in:  strings.NewReader(""),
		out: &out,
	}

	return cli, &out
}

func TestRunFetchesRequestedSignAndPeriod(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args       []string
		wantSign   horoskopy.Sign
		wantPeriod horoskopy.Period
	}{
		"sign only defaults to dnes": {args: []string{"byk"}, wantSign: horoskopy.SignByk, wantPeriod: horoskopy.PeriodDnes},
		"explicit dnes":              {args: []string{"byk", "dnes"}, wantSign: horoskopy.SignByk, wantPeriod: horoskopy.PeriodDnes},
		"tomorrow":                   {args: []string{"ryby", "zitra"}, wantSign: horoskopy.SignRyby, wantPeriod: horoskopy.PeriodZitra},
		"month":                      {args: []string{"lev", "mesic"}, wantSign: horoskopy.SignLev, wantPeriod: horoskopy.PeriodMesic},
		"year":                       {args: []string{"stir", "rok"}, wantSign: horoskopy.SignStir, wantPeriod: horoskopy.PeriodRok},
		"czech diacritics":           {args: []string{"Býk", "zítra"}, wantSign: horoskopy.SignByk, wantPeriod: horoskopy.PeriodZitra},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cli, out := newTestApp(t)
			fetcher, ok := cli.horoscopes.(*fakeFetcher)
			if !ok {
				t.Fatal("test app should use a fake fetcher")
			}

			if err := cli.run(context.Background(), tt.args); err != nil {
				t.Fatalf("run(%v) unexpected error: %v", tt.args, err)
			}
			if fetcher.gotSign != tt.wantSign {
				t.Errorf("run(%v) fetched sign %q, want %q", tt.args, fetcher.gotSign, tt.wantSign)
			}
			if fetcher.gotPeriod != tt.wantPeriod {
				t.Errorf("run(%v) fetched period %q, want %q", tt.args, fetcher.gotPeriod, tt.wantPeriod)
			}
			if !strings.Contains(out.String(), "Dnes to bude dobré.") {
				t.Errorf("run(%v) did not print the horoscope, got %q", tt.args, out.String())
			}
		})
	}
}

func TestRunWithoutArgumentsPrintsUsage(t *testing.T) {
	t.Parallel()

	cli, out := newTestApp(t)
	fetcher, ok := cli.horoscopes.(*fakeFetcher)
	if !ok {
		t.Fatal("test app should use a fake fetcher")
	}

	if err := cli.run(context.Background(), nil); err != nil {
		t.Fatalf("run with no arguments should succeed, got %v", err)
	}
	if fetcher.calls != 0 {
		t.Errorf("run with no arguments should not fetch, got %d calls", fetcher.calls)
	}

	got := out.String()
	for _, want := range []string{"byk", "ryby", "dnes", "zitra", "mesic", "rok", "snar", "login", "logout"} {
		if !strings.Contains(got, want) {
			t.Errorf("usage should mention %q, got %q", want, got)
		}
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args    []string
		wantErr error
	}{
		"unknown sign":   {args: []string{"dymovnica"}, wantErr: horoskopy.ErrUnknownSign},
		"unknown period": {args: []string{"byk", "tyden"}, wantErr: horoskopy.ErrUnknownPeriod},
		"swapped order":  {args: []string{"dnes", "byk"}, wantErr: horoskopy.ErrUnknownSign},
		"too many args":  {args: []string{"byk", "dnes", "navic"}, wantErr: errTooManyArguments},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cli, _ := newTestApp(t)
			fetcher, ok := cli.horoscopes.(*fakeFetcher)
			if !ok {
				t.Fatal("test app should use a fake fetcher")
			}

			err := cli.run(context.Background(), tt.args)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("run(%v) error = %v, want %v", tt.args, err, tt.wantErr)
			}
			if fetcher.calls != 0 {
				t.Errorf("run(%v) should not fetch on bad input, got %d calls", tt.args, fetcher.calls)
			}
		})
	}
}

func TestRunPrintsApologyWhenSiteIsDown(t *testing.T) {
	t.Parallel()

	cli, out := newTestApp(t)
	cli.horoscopes = &fakeFetcher{err: horoskopy.ErrUnavailable}

	err := cli.run(context.Background(), []string{"byk"})
	if !errors.Is(err, horoskopy.ErrUnavailable) {
		t.Fatalf("run error = %v, want ErrUnavailable", err)
	}
	if !strings.Contains(out.String(), "This is fine.") {
		t.Errorf("run should print the apology when the site is down, got %q", out.String())
	}
}

func TestRunDoesNotPrintApologyForBadInput(t *testing.T) {
	t.Parallel()

	cli, out := newTestApp(t)

	if err := cli.run(context.Background(), []string{"dymovnica"}); err == nil {
		t.Fatal("run with an unknown sign should fail, got nil error")
	}
	if strings.Contains(out.String(), "This is fine.") {
		t.Errorf("bad input is not an outage, apology should not be printed, got %q", out.String())
	}
}

func TestRunDreamFromArguments(t *testing.T) {
	t.Parallel()

	cli, out := newTestApp(t)
	dreams, ok := cli.dreams.(*fakeInterpreter)
	if !ok {
		t.Fatal("test app should use a fake interpreter")
	}

	if err := cli.run(context.Background(), []string{"snar", "Zdálo se mi,", "že létám."}); err != nil {
		t.Fatalf("snar unexpected error: %v", err)
	}
	if dreams.gotDream != "Zdálo se mi, že létám." {
		t.Errorf("snar sent dream %q, want the joined arguments", dreams.gotDream)
	}
	if dreams.gotCookie != "cookie-value" {
		t.Errorf("snar sent cookie %q, want the stored one", dreams.gotCookie)
	}
	if !strings.Contains(out.String(), "Ryba znamená intuici.") {
		t.Errorf("snar did not print the interpretation, got %q", out.String())
	}
}

func TestRunDreamFromStdin(t *testing.T) {
	t.Parallel()

	cli, _ := newTestApp(t)
	cli.in = strings.NewReader("Zdálo se mi o rybě.\n")
	dreams, ok := cli.dreams.(*fakeInterpreter)
	if !ok {
		t.Fatal("test app should use a fake interpreter")
	}

	if err := cli.run(context.Background(), []string{"snar"}); err != nil {
		t.Fatalf("snar unexpected error: %v", err)
	}
	if !strings.Contains(dreams.gotDream, "rybě") {
		t.Errorf("snar sent dream %q, want the text from stdin", dreams.gotDream)
	}
}

func TestRunDreamNeedsASession(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		store   *fakeStore
		dreamer *fakeInterpreter
		wantErr error
		wantHit int
	}{
		"no session at all": {
			store:   &fakeStore{loadErr: seznam.ErrNoSession},
			dreamer: &fakeInterpreter{},
			wantErr: seznam.ErrNoSession,
			wantHit: 0,
		},
		"session no longer accepted": {
			store:   &fakeStore{session: seznam.Session{Cookie: "stale"}},
			dreamer: &fakeInterpreter{err: horoskopy.ErrUnauthorized},
			wantErr: horoskopy.ErrUnauthorized,
			wantHit: 1,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cli, _ := newTestApp(t)
			cli.sessions = tt.store
			cli.dreams = tt.dreamer

			err := cli.run(context.Background(), []string{"snar", "Zdálo se mi o rybě."})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("snar error = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), "horoskopycli login") {
				t.Errorf("snar error should say how to fix it, got %q", err)
			}
			if tt.dreamer.calls != tt.wantHit {
				t.Errorf("snar called the API %d times, want %d", tt.dreamer.calls, tt.wantHit)
			}
		})
	}
}

func TestRunLoginStoresVerifiedSession(t *testing.T) {
	t.Parallel()

	cli, out := newTestApp(t)
	store := &fakeStore{}
	cli.sessions = store

	if err := cli.run(context.Background(), []string{"login"}); err != nil {
		t.Fatalf("login unexpected error: %v", err)
	}
	if store.saved == nil {
		t.Fatal("login should store the session")
	}
	if store.saved.Cookie != "browser-cookie" {
		t.Errorf("login stored cookie %q, want the one from the browser", store.saved.Cookie)
	}
	if store.saved.Account != "panbotka@seznam.cz" {
		t.Errorf("login stored account %q, want the verified one", store.saved.Account)
	}
	if !strings.Contains(out.String(), "valid until 2027-09-10") {
		t.Errorf("login should report the validity, got %q", out.String())
	}
}

func TestRunLoginPasteAcceptsCookieForms(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"bare value":     "pasted-cookie",
		"name and value": "ds=pasted-cookie",
		"with semicolon": "ds=pasted-cookie;",
		"with spaces":    "  pasted-cookie  ",
	}

	for name, pasted := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cli, _ := newTestApp(t)
			store := &fakeStore{}
			cli.sessions = store
			cli.in = strings.NewReader(pasted + "\n")
			cli.browserLogin = func(_ context.Context, _ seznam.LoginOptions) (seznam.Session, error) {
				t.Error("login --paste should not open a browser")

				return seznam.Session{}, nil
			}

			if err := cli.run(context.Background(), []string{"login", "--paste"}); err != nil {
				t.Fatalf("login --paste unexpected error: %v", err)
			}
			if store.saved == nil || store.saved.Cookie != "pasted-cookie" {
				t.Fatalf("login --paste stored %+v, want cookie %q", store.saved, "pasted-cookie")
			}
		})
	}
}

func TestRunLoginRejectsASessionThatDoesNotWork(t *testing.T) {
	t.Parallel()

	cli, _ := newTestApp(t)
	store := &fakeStore{}
	cli.sessions = store
	cli.accounts = &fakeAccounts{accountErr: seznam.ErrSessionExpired}

	err := cli.run(context.Background(), []string{"login"})
	if !errors.Is(err, seznam.ErrSessionExpired) {
		t.Fatalf("login error = %v, want ErrSessionExpired", err)
	}
	if store.saved != nil {
		t.Error("login should not store a session that does not work")
	}
}

func TestRunLoginRejectsUnknownFlags(t *testing.T) {
	t.Parallel()

	cli, _ := newTestApp(t)

	if err := cli.run(context.Background(), []string{"login", "--nonsense"}); err == nil {
		t.Fatal("login with an unknown flag should fail, got nil error")
	}
}

func TestRunLogoutRevokesAndForgets(t *testing.T) {
	t.Parallel()

	cli, out := newTestApp(t)
	store := &fakeStore{session: seznam.Session{Cookie: "cookie-value"}}
	accounts := &fakeAccounts{}
	cli.sessions = store
	cli.accounts = accounts

	if err := cli.run(context.Background(), []string{"logout"}); err != nil {
		t.Fatalf("logout unexpected error: %v", err)
	}
	if accounts.revoked != "cookie-value" {
		t.Errorf("logout revoked %q, want the stored cookie", accounts.revoked)
	}
	if !store.forgot {
		t.Error("logout should forget the stored session")
	}
	if !strings.Contains(out.String(), "Logged out") {
		t.Errorf("logout should say so, got %q", out.String())
	}
}

func TestRunLogoutForgetsEvenWhenSeznamCannotBeReached(t *testing.T) {
	t.Parallel()

	cli, out := newTestApp(t)
	store := &fakeStore{session: seznam.Session{Cookie: "cookie-value"}}
	cli.sessions = store
	cli.accounts = &fakeAccounts{revokeErr: seznam.ErrLoginService}

	if err := cli.run(context.Background(), []string{"logout"}); err != nil {
		t.Fatalf("logout should not fail when Seznam is unreachable, got %v", err)
	}
	if !store.forgot {
		t.Error("logout should forget the session even when the revoke failed")
	}
	if !strings.Contains(out.String(), "could not be told") {
		t.Errorf("logout should admit the session may still be alive, got %q", out.String())
	}
}

func TestRunLogoutWithoutSession(t *testing.T) {
	t.Parallel()

	cli, out := newTestApp(t)
	store := &fakeStore{loadErr: seznam.ErrNoSession}
	accounts := &fakeAccounts{}
	cli.sessions = store
	cli.accounts = accounts

	if err := cli.run(context.Background(), []string{"logout"}); err != nil {
		t.Fatalf("logout without a session should succeed, got %v", err)
	}
	if accounts.revoked != "" {
		t.Error("logout without a session should not call Seznam")
	}
	if !strings.Contains(out.String(), "Not logged in.") {
		t.Errorf("logout should say there is nothing to do, got %q", out.String())
	}
}

func TestExpiryNote(t *testing.T) {
	t.Parallel()

	if got := expiryNote(time.Time{}); got != "" {
		t.Errorf("expiryNote(zero) = %q, want empty", got)
	}

	want := ", valid until 2027-09-10"
	if got := expiryNote(time.Date(2027, 9, 10, 23, 2, 1, 0, time.UTC)); got != want {
		t.Errorf("expiryNote() = %q, want %q", got, want)
	}
}

func TestCleanCookie(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"value":      "value",
		"ds=value":   "value",
		"ds=value;":  "value",
		"  value  ":  "value",
		"ds=value ;": "value ", // only a trailing semicolon is stripped, spaces inside stay
	}

	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			if got := cleanCookie(input); got != want {
				t.Errorf("cleanCookie(%q) = %q, want %q", input, got, want)
			}
		})
	}
}
