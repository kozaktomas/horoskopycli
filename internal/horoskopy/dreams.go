package horoskopy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"
)

// The dream book is the one part of horoskopy.cz that is not public: it runs a
// language model per request, so Seznam meters it per account. The endpoint
// takes the Seznam session cookie and nothing else — see internal/seznam for
// how that cookie is obtained.

// sessionCookieName is the Seznam single sign-on cookie the API authenticates
// with. It is named separately here so this package stays independent of the
// login flow; internal/seznam owns the cookie itself.
const sessionCookieName = "ds"

// dreamMinRunes and dreamMaxRunes are the bounds the website itself enforces
// before it will send a dream off for interpretation.
const (
	dreamMinRunes = 3
	dreamMaxRunes = 2000
)

// ErrDreamTooShort is returned when a dream is too short to interpret.
var ErrDreamTooShort = errors.New("dream is too short")

// ErrDreamTooLong is returned when a dream exceeds the length the API accepts.
var ErrDreamTooLong = errors.New("dream is too long")

// ErrUnauthorized is returned when the session cookie is missing, expired or
// rejected, which is the API's way of saying: log in again.
var ErrUnauthorized = errors.New("horoskopy.cz rejected the session")

// ErrEmptyInterpretation is returned when the API answers without any text.
var ErrEmptyInterpretation = errors.New("horoskopy.cz returned an empty interpretation")

// InterpretDream sends a dream to the horoskopy.cz dream book and returns its
// interpretation. The cookie is a Seznam session as stored by internal/seznam.
//
// It returns ErrDreamTooShort or ErrDreamTooLong for dreams the API would
// refuse anyway, ErrUnauthorized when the session is no longer valid, and
// ErrUnavailable when horoskopy.cz cannot be reached.
func (c *Client) InterpretDream(ctx context.Context, cookie, dream string) (string, error) {
	if err := validateDream(dream); err != nil {
		return "", err
	}

	res, err := c.postDream(ctx, cookie, dream)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = res.Body.Close()
	}()

	url := c.baseURL + "/v1/dream-book"

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrUnauthorized
	default:
		return "", fmt.Errorf("%s returned status %d: %w", url, res.StatusCode, ErrUnavailable)
	}

	var answer struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&answer); err != nil {
		return "", fmt.Errorf("could not decode response from %s: %w", url, err)
	}

	interpretation := sanitizeText(answer.Result)
	if interpretation == "" {
		return "", ErrEmptyInterpretation
	}

	return interpretation, nil
}

// postDream sends the dream to the API with the session cookie attached.
func (c *Client) postDream(ctx context.Context, cookie, dream string) (*http.Response, error) {
	url := c.baseURL + "/v1/dream-book"

	body, err := json.Marshal(map[string]string{"dream": dream})
	if err != nil {
		return nil, fmt.Errorf("could not encode the dream: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("could not build request for %s: %w", url, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w: %w", url, ErrUnavailable, err)
	}

	return res, nil
}

// validateDream checks a dream against the bounds the API enforces, so an
// obviously doomed request is refused before it is sent.
func validateDream(dream string) error {
	dream = sanitizeText(dream)

	switch length := utf8.RuneCountInString(dream); {
	case length < dreamMinRunes:
		return fmt.Errorf("%w: %d characters, at least %d are needed", ErrDreamTooShort, length, dreamMinRunes)
	case length > dreamMaxRunes:
		return fmt.Errorf("%w: %d characters, at most %d are allowed", ErrDreamTooLong, length, dreamMaxRunes)
	}

	return nil
}
