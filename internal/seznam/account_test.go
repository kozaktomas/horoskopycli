package seznam

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestAPI serves the given handler and returns an API pointed at it.
func newTestAPI(t *testing.T, handler http.HandlerFunc) *API {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	api := NewAPI()
	api.badgeURL = server.URL + "/api/v1/user/badge"
	api.logoutURL = server.URL + "/logout"

	return api
}

// writeBody sends a canned response body from a test HTTP handler.
func writeBody(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()

	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("could not write test response: %v", err)
	}
}

func TestAPIAccountReadsTheBadge(t *testing.T) {
	t.Parallel()

	var gotCookie string
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(CookieName); err == nil {
			gotCookie = cookie.Value
		}

		writeBody(t, w, `{"state":"green","users":[{"accountDisplayName":"panbotka@seznam.cz","state":"green"}]}`)
	})

	account, err := api.Account(context.Background(), "cookie-value")
	if err != nil {
		t.Fatalf("Account returned %v", err)
	}
	if account != "panbotka@seznam.cz" {
		t.Errorf("Account() = %q, want the display name", account)
	}
	if gotCookie != "cookie-value" {
		t.Errorf("Account sent cookie %q, want the session", gotCookie)
	}
}

func TestAPIAccountRecognisesAnEndedSession(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"logged out": `{"state":"red","users":[],"flags":{}}`,
		"no users":   `{"state":"green","users":[]}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			api := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
				writeBody(t, w, body)
			})

			if _, err := api.Account(context.Background(), "cookie-value"); !errors.Is(err, ErrSessionExpired) {
				t.Errorf("Account() = %v, want ErrSessionExpired", err)
			}
		})
	}
}

func TestAPIAccountReportsABrokenService(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status int
		body   string
	}{
		"server error": {status: http.StatusInternalServerError, body: `{}`},
		"not JSON":     {status: http.StatusOK, body: `<html>`},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			api := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				writeBody(t, w, tt.body)
			})

			if _, err := api.Account(context.Background(), "cookie-value"); !errors.Is(err, ErrLoginService) {
				t.Errorf("Account() = %v, want ErrLoginService", err)
			}
		})
	}
}

func TestAPIRevokePostsTheSession(t *testing.T) {
	t.Parallel()

	var (
		gotMethod      string
		gotCookie      string
		gotContentType string
		gotBody        string
	)

	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")

		if cookie, err := r.Cookie(CookieName); err == nil {
			gotCookie = cookie.Value
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("could not read the request body: %v", err)
		}
		gotBody = string(body)

		writeBody(t, w, `{}`)
	})

	if err := api.Revoke(context.Background(), "cookie-value"); err != nil {
		t.Fatalf("Revoke returned %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("Revoke used %s, want POST: a GET is refused by the login service", gotMethod)
	}
	if gotCookie != "cookie-value" {
		t.Errorf("Revoke sent cookie %q, want the session", gotCookie)
	}
	// The real service answers 400 without these two.
	if gotBody != "{}" {
		t.Errorf("Revoke sent body %q, want an empty JSON object", gotBody)
	}
	if gotContentType != "application/json" {
		t.Errorf("Revoke sent Content-Type %q, want application/json", gotContentType)
	}
}

func TestAPIRevokeReportsARefusal(t *testing.T) {
	t.Parallel()

	api := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	if err := api.Revoke(context.Background(), "cookie-value"); !errors.Is(err, ErrLoginService) {
		t.Errorf("Revoke() = %v, want ErrLoginService", err)
	}
}

func TestAPIReportsAnUnreachableService(t *testing.T) {
	t.Parallel()

	api := NewAPI()
	api.badgeURL = "http://127.0.0.1:0/api/v1/user/badge"
	api.logoutURL = "http://127.0.0.1:0/logout"

	if _, err := api.Account(context.Background(), "cookie-value"); !errors.Is(err, ErrLoginService) {
		t.Errorf("Account() = %v, want ErrLoginService", err)
	}
	if err := api.Revoke(context.Background(), "cookie-value"); !errors.Is(err, ErrLoginService) {
		t.Errorf("Revoke() = %v, want ErrLoginService", err)
	}
}
