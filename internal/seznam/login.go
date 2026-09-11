package seznam

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// loginURL is the Seznam login screen, told which service is asking and where
// to return afterwards. Landing back on horoskopy.cz is what mints the cookie
// for that domain.
const loginURL = "https://login.seznam.cz/?service=horoskopy" +
	"&return_url=https%3A%2F%2Fwww.horoskopy.cz%2Fp%2Fsnar-vyklad-snu"

// handoffURL is opened when the user is signed in to Seznam but has not been
// returned to horoskopy.cz, which happens when Seznam interrupts the flow with
// a screen of its own.
const handoffURL = "https://www.horoskopy.cz/"

// DefaultLoginTimeout is how long the browser stays open waiting for the user
// to finish signing in.
const DefaultLoginTimeout = 5 * time.Minute

// browserStartTimeout bounds how long the browser may take to open its
// debugging port.
const browserStartTimeout = 30 * time.Second

// cookiePollInterval is how often the browser cookie jar is inspected while
// waiting for the user.
const cookiePollInterval = time.Second

// stuckHint is how long to wait with nothing happening before suggesting that
// the browser window may not have appeared at all.
const stuckHint = 45 * time.Second

// ErrLoginTimeout is returned when the login was not completed in time.
var ErrLoginTimeout = errors.New("timed out waiting for the login to finish")

// cookieSource is a browser this CLI started and can question. Chromium-based
// browsers answer through cdpConn, Firefox through bidiConn.
type cookieSource interface {
	// cookies returns every cookie the browser currently holds.
	cookies() ([]cookie, error)
	// openTab opens a URL in a new tab.
	openTab(url string) error
	// closeBrowser asks the browser to shut itself down.
	closeBrowser() error
	// close releases the connection to the browser.
	close() error
}

// LoginOptions tunes how Login runs.
type LoginOptions struct {
	// Browser is the browser executable to use. Empty means: find one.
	Browser string
	// Timeout is how long to wait for the user. Zero means DefaultLoginTimeout.
	Timeout time.Duration
	// Progress receives a line of reassurance while the browser is open. It
	// may be nil.
	Progress io.Writer
	// Debug asks for a line per poll saying what the browser is holding, for
	// working out why a login never completes.
	Debug bool
}

// progress reports what the login is doing, and says nothing when the caller
// asked for no output.
type progress struct {
	out   io.Writer
	debug bool
}

// printf writes a line for the person waiting.
func (p progress) printf(format string, args ...any) {
	if p.out == nil {
		return
	}

	fmt.Fprintf(p.out, format, args...)
}

// debugf writes a line only when debugging was asked for.
func (p progress) debugf(format string, args ...any) {
	if !p.debug {
		return
	}

	p.printf(format, args...)
}

// Login opens a browser on the Seznam login screen, waits for the user to sign
// in, and returns the session cookie the browser was given. The browser runs
// on a throwaway profile which is deleted afterwards, so nothing but the
// cookie survives.
//
// It returns ErrNoBrowser when no browser this CLI can drive is installed, and
// ErrLoginTimeout when the user does not finish in time.
func Login(ctx context.Context, opts LoginOptions) (Session, error) {
	found, err := findBrowser(opts.Browser)
	if err != nil {
		return Session{}, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultLoginTimeout
	}

	started, err := launchBrowser(ctx, found, browserStartTimeout)
	if err != nil {
		return Session{}, err
	}
	defer started.stop()

	conn, err := started.connect()
	if err != nil {
		return Session{}, err
	}
	defer func() {
		// Ask the browser to leave before killing it, so that it takes its
		// helper processes — and the profile holding the login — with it.
		//nolint:errcheck // best effort: the process is killed right after anyway.
		_ = conn.closeBrowser()
		_ = conn.close()
	}()

	report := progress{out: opts.Progress, debug: opts.Debug}
	report.debugf("Browser: %s, driven at %s\n", found.path, started.endpoint)

	// The login page is opened here rather than passed on the command line,
	// so that it cannot land in a browser window this CLI cannot read.
	if err := conn.openTab(loginURL); err != nil {
		return Session{}, fmt.Errorf("could not open the login page: %w", err)
	}

	report.printf("Waiting for the Seznam login in %s…\n", found.path)

	return waitForSession(ctx, conn, timeout, cookiePollInterval, report)
}

// waitForSession polls the browser cookie jar every interval until the
// horoskopy.cz session cookie appears, nudging a stalled login along the way.
func waitForSession(
	ctx context.Context,
	conn cookieSource,
	timeout, interval time.Duration,
	report progress,
) (Session, error) {
	deadline := time.Now().Add(timeout)
	started := time.Now()
	nudged := false
	hinted := false

	for time.Now().Before(deadline) {
		jar, err := conn.cookies()
		if err != nil {
			return Session{}, err
		}

		report.debugf("  %s\n", describeJar(jar))

		if !hinted && !signedIn(jar) && time.Since(started) > stuckHint {
			report.printf("Still waiting. If no browser window opened, press Ctrl-C and try " +
				"`horoskopycli login --paste`, or `--browser <path>` to pick another browser.\n")
			hinted = true
		}

		if found, ok := sessionCookie(jar); ok {
			return Session{Cookie: found.Value, Expires: found.expiresAt()}, nil
		}

		// Signed in to Seznam, but not carried over to horoskopy.cz yet:
		// opening the site once is what hands the session over.
		if !nudged && signedIn(jar) {
			report.debugf("  signed in to Seznam, opening %s to hand the session over\n", handoffURL)

			if err := conn.openTab(handoffURL); err != nil {
				return Session{}, err
			}
			nudged = true
		}

		if err := sleep(ctx, interval); err != nil {
			return Session{}, err
		}
	}

	return Session{}, fmt.Errorf("%w after %s", ErrLoginTimeout, timeout)
}

// describeJar summarises a cookie jar for the debug output: the only thing
// worth watching is where the session cookie has appeared so far.
func describeJar(jar []cookie) string {
	domains := make([]string, 0, 2)
	for _, candidate := range jar {
		if candidate.Name == CookieName {
			domains = append(domains, candidate.Domain)
		}
	}

	if len(domains) == 0 {
		return fmt.Sprintf("%d cookies, no Seznam session yet", len(jar))
	}

	return fmt.Sprintf("%d cookies, Seznam session on %s", len(jar), strings.Join(domains, ", "))
}

// sessionCookie picks the horoskopy.cz session cookie out of a cookie jar.
func sessionCookie(jar []cookie) (cookie, bool) {
	for _, candidate := range jar {
		if candidate.Name == CookieName && inDomain(candidate.Domain, cookieDomain) {
			return candidate, true
		}
	}

	return cookie{}, false
}

// signedIn reports whether the jar holds a Seznam session for any domain,
// which means the user is through the login screen.
func signedIn(jar []cookie) bool {
	for _, candidate := range jar {
		if candidate.Name == CookieName {
			return true
		}
	}

	return false
}

// inDomain reports whether a cookie domain is the given domain or one of its
// subdomains, without matching lookalikes such as nothoroskopy.cz.
func inDomain(candidate, domain string) bool {
	candidate = strings.TrimPrefix(candidate, ".")

	return candidate == domain || strings.HasSuffix(candidate, "."+domain)
}

// sleep waits for the given duration, returning early if the context is done.
func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("login interrupted: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
