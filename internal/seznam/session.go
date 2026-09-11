// Package seznam signs the CLI in to a Seznam account and keeps the resulting
// session, so that the parts of horoskopy.cz which are gated on a login can be
// used from a terminal.
//
// Seznam has an official OAuth 2.0 service, but its tokens only ever open
// login.seznam.cz/api/v1/user. The horoskopy.cz API authenticates with the
// first-party SSO cookie instead, so signing in means running a real browser
// and keeping the cookie it is given.
package seznam

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CookieName is the Seznam single sign-on cookie. It is the only credential
// horoskopy.cz looks at, so it is also what a user pastes in when this CLI
// cannot drive their browser.
const CookieName = "ds"

// cookieDomain is the domain the cookie has to be scoped to: the API lives on
// api-web.horoskopy.cz, so a cookie for seznam.cz alone would never reach it.
const cookieDomain = "horoskopy.cz"

// EnvCookie overrides the stored session, for scripts and CI that would rather
// pass the cookie in than keep a file.
const EnvCookie = "HOROSKOPYCLI_DS"

// sessionDirPerm and sessionFilePerm keep the session readable by its owner
// only: the cookie authenticates the whole Seznam account, not just horoscopes.
const (
	sessionDirPerm  os.FileMode = 0o700
	sessionFilePerm os.FileMode = 0o600
)

// ErrNoSession is returned when no session has been stored yet.
var ErrNoSession = errors.New("not logged in")

// Session is a stored Seznam login.
type Session struct {
	Cookie  string    `json:"cookie"`
	Account string    `json:"account,omitempty"`
	Expires time.Time `json:"expires,omitempty"`
}

// Load returns the stored session, or the one named by EnvCookie when that
// variable is set. It returns ErrNoSession when neither is available.
func Load() (Session, error) {
	if cookie := os.Getenv(EnvCookie); cookie != "" {
		return Session{Cookie: cookie}, nil
	}

	path, err := SessionPath()
	if err != nil {
		return Session{}, err
	}

	raw, err := os.ReadFile(path) //nolint:gosec // path is this CLI's own config file.
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, fmt.Errorf("could not read %s: %w", path, err)
	}

	var session Session
	if err := json.Unmarshal(raw, &session); err != nil {
		return Session{}, fmt.Errorf("could not decode %s: %w", path, err)
	}

	if session.Cookie == "" {
		return Session{}, ErrNoSession
	}

	return session, nil
}

// Save writes the session to disk, creating the config directory if needed.
func (s Session) Save() error {
	path, err := SessionPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), sessionDirPerm); err != nil {
		return fmt.Errorf("could not create %s: %w", filepath.Dir(path), err)
	}

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode session: %w", err)
	}

	if err := os.WriteFile(path, append(raw, '\n'), sessionFilePerm); err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}

	return nil
}

// Forget removes the stored session file. It succeeds when there is nothing to
// remove, so that logging out twice is not an error.
func Forget() error {
	path, err := SessionPath()
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not remove %s: %w", path, err)
	}

	return nil
}

// SessionPath returns the file the session is kept in, honouring the usual
// per-user configuration directory of the platform.
func SessionPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not locate the user config directory: %w", err)
	}

	return filepath.Join(dir, "horoskopycli", "session.json"), nil
}
