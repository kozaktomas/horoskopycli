package seznam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// badgeURL reports who, if anyone, a horoskopy.cz session belongs to. It is
// the same endpoint the website itself uses to decide whether to show the
// login button.
const badgeURL = "https://login.horoskopy.cz/api/v1/user/badge?service=horoskopy"

// logoutURL revokes a session server-side. Note the method: a GET here answers
// 403, only a POST logs out.
const logoutURL = "https://login.horoskopy.cz/logout?service=horoskopy"

// stateLoggedIn is the badge state of a signed-in user; Seznam calls it green.
const stateLoggedIn = "green"

// requestTimeout bounds a single call to the login service.
const requestTimeout = 15 * time.Second

// ErrSessionExpired is returned when a stored cookie is no longer signed in.
var ErrSessionExpired = errors.New("seznam session expired")

// ErrLoginService is returned when login.horoskopy.cz cannot be reached or
// answers unexpectedly.
var ErrLoginService = errors.New("seznam login service is unavailable")

// API talks to the Seznam login service that fronts horoskopy.cz.
type API struct {
	httpClient *http.Client
	badgeURL   string
	logoutURL  string
}

// NewAPI returns an API pointed at the public login service.
func NewAPI() *API {
	return &API{
		httpClient: &http.Client{Timeout: requestTimeout},
		badgeURL:   badgeURL,
		logoutURL:  logoutURL,
	}
}

// badge is the part of the badge response describing the current user.
type badge struct {
	State string `json:"state"`
	Users []struct {
		AccountDisplayName string `json:"accountDisplayName"`
	} `json:"users"`
}

// Account returns the display name of the account a cookie belongs to, which
// doubles as a check that the cookie still works. It returns ErrSessionExpired
// when the cookie is not signed in.
func (a *API) Account(ctx context.Context, cookie string) (string, error) {
	res, err := a.do(ctx, http.MethodGet, a.badgeURL, cookie)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned status %d: %w", a.badgeURL, res.StatusCode, ErrLoginService)
	}

	var answer badge
	if err := json.NewDecoder(res.Body).Decode(&answer); err != nil {
		return "", fmt.Errorf("could not decode the badge response: %w: %w", ErrLoginService, err)
	}

	if answer.State != stateLoggedIn || len(answer.Users) == 0 {
		return "", ErrSessionExpired
	}

	return strings.TrimSpace(answer.Users[0].AccountDisplayName), nil
}

// Revoke ends the session server-side, so that a cookie which has been copied
// somewhere else stops working too. Deleting the local file alone would not do
// that.
//
// The login service is particular about the request: a GET is answered with
// 403 and a POST without a JSON body with 400, so it gets an empty object.
func (a *API) Revoke(ctx context.Context, cookie string) error {
	res, err := a.do(ctx, http.MethodPost, a.logoutURL, cookie)
	if err != nil {
		return err
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned status %d: %w", a.logoutURL, res.StatusCode, ErrLoginService)
	}

	return nil
}

// do performs a request to the login service carrying the session cookie. A
// POST is given an empty JSON object, which the service insists on.
func (a *API) do(ctx context.Context, method, url, cookie string) (*http.Response, error) {
	body := io.Reader(http.NoBody)
	if method == http.MethodPost {
		body = strings.NewReader("{}")
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("could not build request for %s: %w", url, err)
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})

	res, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w: %w", url, ErrLoginService, err)
	}

	return res, nil
}
