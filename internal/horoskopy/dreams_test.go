package horoskopy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClientInterpretDreamSendsSessionAndDream(t *testing.T) {
	t.Parallel()

	var (
		gotMethod string
		gotPath   string
		gotCookie string
		gotBody   map[string]string
	)

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path

		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			gotCookie = cookie.Value
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("could not read the request body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}

		writeBody(t, w, []byte(`{"result":"Ryba  symbolizuje\nintuici."}`))
	})

	interpretation, err := client.InterpretDream(context.Background(), "cookie-value", "Zdálo se mi o rybě.")
	if err != nil {
		t.Fatalf("InterpretDream returned an unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("InterpretDream used %s, want POST", gotMethod)
	}
	if gotPath != "/v1/dream-book" {
		t.Errorf("InterpretDream called %q, want /v1/dream-book", gotPath)
	}
	if gotCookie != "cookie-value" {
		t.Errorf("InterpretDream sent cookie %q, want the session", gotCookie)
	}
	if gotBody["dream"] != "Zdálo se mi o rybě." {
		t.Errorf("InterpretDream sent dream %q", gotBody["dream"])
	}
	if interpretation != "Ryba symbolizuje intuici." {
		t.Errorf("InterpretDream = %q, want the tidied interpretation", interpretation)
	}
}

func TestClientInterpretDreamHandlesRefusals(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status  int
		body    string
		wantErr error
	}{
		"unauthorized":       {status: http.StatusUnauthorized, body: `{"message":"Unauthorized access"}`, wantErr: ErrUnauthorized},
		"forbidden":          {status: http.StatusForbidden, body: `{}`, wantErr: ErrUnauthorized},
		"server error":       {status: http.StatusInternalServerError, body: `{}`, wantErr: ErrUnavailable},
		"empty result":       {status: http.StatusOK, body: `{"result":""}`, wantErr: ErrEmptyInterpretation},
		"whitespace result":  {status: http.StatusOK, body: `{"result":"   "}`, wantErr: ErrEmptyInterpretation},
		"result is missing":  {status: http.StatusOK, body: `{}`, wantErr: ErrEmptyInterpretation},
		"answer is not JSON": {status: http.StatusOK, body: `<html>`, wantErr: nil},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				writeBody(t, w, []byte(tt.body))
			})

			_, err := client.InterpretDream(context.Background(), "cookie-value", "Zdálo se mi o rybě.")
			if err == nil {
				t.Fatal("InterpretDream should have failed")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("InterpretDream error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestClientInterpretDreamValidatesLengthBeforeCalling(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dream   string
		wantErr error
	}{
		"empty":       {dream: "", wantErr: ErrDreamTooShort},
		"whitespace":  {dream: "  \n ", wantErr: ErrDreamTooShort},
		"two letters": {dream: "ah", wantErr: ErrDreamTooShort},
		"too long":    {dream: strings.Repeat("á", dreamMaxRunes+1), wantErr: ErrDreamTooLong},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			called := false
			client := newTestClient(t, func(_ http.ResponseWriter, _ *http.Request) {
				called = true
			})

			_, err := client.InterpretDream(context.Background(), "cookie-value", tt.dream)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("InterpretDream(%q) error = %v, want %v", tt.dream, err, tt.wantErr)
			}
			if called {
				t.Error("InterpretDream should not call the API with a dream it knows is refused")
			}
		})
	}
}

func TestClientInterpretDreamAcceptsBoundaryLengths(t *testing.T) {
	t.Parallel()

	for name, dream := range map[string]string{
		"shortest allowed": strings.Repeat("á", dreamMinRunes),
		"longest allowed":  strings.Repeat("á", dreamMaxRunes),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				writeBody(t, w, []byte(`{"result":"Výklad."}`))
			})

			if _, err := client.InterpretDream(context.Background(), "cookie-value", dream); err != nil {
				t.Errorf("InterpretDream(%d runes) returned %v, want success", len([]rune(dream)), err)
			}
		})
	}
}

func TestRenderDream(t *testing.T) {
	t.Parallel()

	want := strings.Join([]string{
		"Výklad snu",
		"==========",
		"",
		"Ryba symbolizuje intuici.",
		"",
		dreamBookURL,
		"",
	}, "\n")

	if got := RenderDream("Ryba symbolizuje intuici."); got != want {
		t.Errorf("RenderDream() =\n%q\nwant\n%q", got, want)
	}
}
