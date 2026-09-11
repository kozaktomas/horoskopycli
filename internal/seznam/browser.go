package seznam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// engine is the automation protocol a browser speaks. Chrome and its
// relatives speak the DevTools protocol; Firefox speaks WebDriver BiDi.
type engine int

const (
	engineChromium engine = iota
	engineFirefox
)

// engineNames maps a substring of an executable name to the protocol its
// browser speaks. Everything Chromium-based answers to the DevTools protocol,
// and every Firefox fork to BiDi.
var engineNames = map[string]engine{
	"firefox":   engineFirefox,
	"waterfox":  engineFirefox,
	"floorp":    engineFirefox,
	"librewolf": engineFirefox,
	"chrome":    engineChromium,
	"chromium":  engineChromium,
	"brave":     engineChromium,
	"edge":      engineChromium,
	"vivaldi":   engineChromium,
	"opera":     engineChromium,
}

// browserCandidates are the browsers looked for in PATH, in order of
// preference. Firefox comes first only because a Firefox user rarely has
// Chrome installed as well, while the reverse is common.
var browserCandidates = []string{
	"firefox",
	"google-chrome",
	"google-chrome-stable",
	"chromium",
	"chromium-browser",
	"brave-browser",
	"microsoft-edge",
}

// browserExitTimeout is how long a browser that has been asked to close is
// given to do so before it is killed.
const browserExitTimeout = 5 * time.Second

// portProbeInterval and portProbeTimeout pace the check for whether the
// browser is still listening on its remote control port.
const (
	portProbeInterval = 200 * time.Millisecond
	portProbeTimeout  = time.Second
)

// profileRemovalDelay is how long to wait before another attempt at deleting
// the throwaway profile, giving a browser's helpers time to finish writing.
const profileRemovalDelay = 500 * time.Millisecond

// profileRemovalAttempts is how many times deleting the profile is retried
// before giving up and leaving it to the operating system's temporary
// directory cleanup.
const profileRemovalAttempts = 3

// ErrNoBrowser is returned when no supported browser can be found to run the
// login in.
var ErrNoBrowser = errors.New("no supported browser found")

// ErrNoDebugPort is returned when a browser starts but never opens its remote
// control port, so its cookies cannot be read.
var ErrNoDebugPort = errors.New("browser did not open a debugging port")

// foundBrowser is a browser executable and the protocol it speaks.
type foundBrowser struct {
	path   string
	engine engine
}

// browser is a browser process started by this CLI, together with the
// throwaway profile it was given and the endpoint it is driven through.
type browser struct {
	cmd      *exec.Cmd
	profile  string
	endpoint string
	engine   engine
}

// engineFor works out which protocol the executable at path speaks, and
// reports false for a browser this CLI cannot drive, such as Safari.
func engineFor(path string) (engine, bool) {
	name := strings.ToLower(filepath.Base(path))
	name = strings.TrimSuffix(name, ".exe")

	for fragment, spoken := range engineNames {
		if strings.Contains(name, fragment) {
			return spoken, true
		}
	}

	return engineChromium, false
}

// findBrowser returns the browser to run the login in: the one asked for, else
// the user's default browser, else the first supported one installed.
//
// It returns ErrNoBrowser when nothing usable is found.
func findBrowser(preferred string) (foundBrowser, error) {
	if preferred != "" {
		path, err := exec.LookPath(preferred)
		if err != nil {
			return foundBrowser{}, fmt.Errorf("could not use browser %q: %w", preferred, err)
		}

		return describeBrowser(path), nil
	}

	if path, err := defaultBrowserPath(); err == nil {
		if _, supported := engineFor(path); supported {
			return describeBrowser(path), nil
		}
	}

	for _, candidate := range browserCandidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return describeBrowser(path), nil
		}
	}

	for _, candidate := range installedBrowserPaths() {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return describeBrowser(candidate), nil
		}
	}

	return foundBrowser{}, fmt.Errorf("%w: tried %s", ErrNoBrowser, strings.Join(browserCandidates, ", "))
}

// describeBrowser pairs an executable with the protocol it speaks. An unknown
// browser is assumed to be Chromium-based, which is the safer guess: every
// mainstream browser except Firefox and Safari is.
func describeBrowser(path string) foundBrowser {
	spoken, _ := engineFor(path)

	return foundBrowser{path: path, engine: spoken}
}

// launchBrowser starts a browser on a fresh throwaway profile with remote
// control enabled, and waits until it can be driven. The caller must call stop
// to shut it down and delete the profile.
func launchBrowser(ctx context.Context, found foundBrowser, timeout time.Duration) (*browser, error) {
	profile, err := newProfile(found)
	if err != nil {
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		_ = os.RemoveAll(profile)

		return nil, err
	}

	attempts := launchAttempts(ctx, found, profile, port)
	perAttempt := timeout / time.Duration(len(attempts))

	var lastErr error

	for _, cmd := range attempts {
		started, err := tryLaunch(cmd, found.engine, profile, port, perAttempt)
		if err == nil {
			return started, nil
		}

		lastErr = err
	}

	_ = os.RemoveAll(profile)

	return nil, lastErr
}

// tryLaunch starts one candidate command and waits for the browser to answer
// on its port, cleaning the process up again if it never does.
func tryLaunch(cmd *exec.Cmd, spoken engine, profile string, port int, timeout time.Duration) (*browser, error) {
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start %s: %w", cmd.Path, err)
	}

	started := &browser{cmd: cmd, profile: profile, engine: spoken}

	endpoint, err := waitForEndpoint(spoken, port, timeout)
	if err != nil {
		started.waitOrKill(0)

		return nil, err
	}
	started.endpoint = endpoint

	return started, nil
}

// freePort asks the operating system for a port the browser can listen on.
//
// The port is released again before the browser claims it, which leaves a
// small window for something else to take it. That is why a browser failing to
// appear on the port is reported rather than waited on forever.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("could not find a free port: %w", err)
	}

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()

		return 0, fmt.Errorf("could not find a free port: %w", ErrNoDebugPort)
	}

	if err := listener.Close(); err != nil {
		return 0, fmt.Errorf("could not release the chosen port: %w", err)
	}

	return address.Port, nil
}

// browserArgs builds the command line that puts a browser under remote control
// on a profile of its own.
//
// No address is passed: the login page is opened afterwards, over the
// protocol. A URL on the command line can be picked up by a browser that is
// already running instead — and then the person signs in to a window this CLI
// cannot see.
func browserArgs(spoken engine, profile string, port int) []string {
	remoteControl := fmt.Sprintf("--remote-debugging-port=%d", port)

	if spoken == engineFirefox {
		// -no-remote implies a new instance, so an already running Firefox
		// neither swallows this window nor is disturbed by it.
		return []string{
			"--profile", profile,
			"--no-remote",
			remoteControl,
		}
	}

	return []string{
		"--user-data-dir=" + profile,
		remoteControl,
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=520,780",
	}
}

// newProfile creates the throwaway profile directory.
//
// Snap-packaged browsers cannot see the system /tmp — each snap gets one of
// its own — nor hidden directories in the home directory, so for those the
// profile has to live inside the snap's own directory.
func newProfile(found foundBrowser) (string, error) {
	parent := ""
	if snapDir, ok := snapProfileDir(found.path); ok {
		parent = snapDir
	}

	profile, err := os.MkdirTemp(parent, "horoskopycli-login-")
	if err != nil {
		return "", fmt.Errorf("could not create a browser profile: %w", err)
	}

	return profile, nil
}

// snapProfileDir returns the directory a snap-confined browser can read, if
// the browser looks like a snap.
func snapProfileDir(path string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}

	name := strings.ToLower(filepath.Base(path))

	for _, snap := range []string{"firefox", "chromium"} {
		if !strings.Contains(name, snap) {
			continue
		}

		dir := filepath.Join(home, "snap", snap, "common")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, true
		}
	}

	return "", false
}

// waitForEndpoint waits for the browser to start answering on its remote
// control port, and returns the address it can be driven through.
func waitForEndpoint(spoken engine, port int, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if endpoint, ok := probeEndpoint(spoken, port); ok {
			return endpoint, nil
		}

		time.Sleep(portProbeInterval)
	}

	return "", fmt.Errorf("%w within %s", ErrNoDebugPort, timeout)
}

// probeEndpoint asks the port whether a browser is there yet.
//
// Firefox serves WebDriver BiDi at a fixed path, so a connection is answer
// enough. A Chromium-based browser has to be asked: its browser-level socket
// carries an identifier only it knows.
func probeEndpoint(spoken engine, port int) (string, bool) {
	if spoken == engineFirefox {
		conn, err := net.DialTimeout("tcp", localAddress(port), portProbeTimeout)
		if err != nil {
			return "", false
		}
		_ = conn.Close()

		return fmt.Sprintf("ws://%s%s", localAddress(port), bidiSessionPath), true
	}

	return devToolsEndpoint(port)
}

// devToolsEndpoint reads the browser-level WebSocket address out of a
// Chromium-based browser's own description of itself.
func devToolsEndpoint(port int) (string, bool) {
	client := &http.Client{Timeout: portProbeTimeout}

	res, err := client.Get(fmt.Sprintf("http://%s/json/version", localAddress(port)))
	if err != nil {
		return "", false
	}
	defer func() {
		_ = res.Body.Close()
	}()

	var described struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(res.Body).Decode(&described); err != nil {
		return "", false
	}

	return described.WebSocketDebuggerURL, described.WebSocketDebuggerURL != ""
}

// localAddress is the loopback address the browser was told to listen on.
func localAddress(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// connect opens a driving session with the browser, whichever protocol it
// speaks.
func (b *browser) connect() (cookieSource, error) {
	if b.engine == engineFirefox {
		return dialBiDi(b.endpoint)
	}

	return dialCDP(b.endpoint)
}

// stop shuts the browser down and removes its throwaway profile, taking the
// login session with it: the cookie the CLI kept is the only thing that
// survives.
func (b *browser) stop() {
	b.waitForPort(browserExitTimeout)
	b.waitOrKill(browserExitTimeout)
	b.removeProfile()
}

// waitForPort waits until the browser stops answering on its remote control
// port, which is when it has really gone.
//
// The process this CLI started is not always the browser: a snap wrapper or a
// launcher script exits immediately and leaves the real browser running behind
// it, still writing to the profile that is about to be deleted.
func (b *browser) waitForPort(timeout time.Duration) {
	address, ok := endpointAddress(b.endpoint)
	if !ok {
		return
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, portProbeTimeout)
		if err != nil {
			return // nothing is listening any more
		}
		_ = conn.Close()

		time.Sleep(portProbeInterval)
	}
}

// endpointAddress takes the host and port out of a ws:// endpoint.
func endpointAddress(endpoint string) (string, bool) {
	rest, found := strings.CutPrefix(endpoint, "ws://")
	if !found {
		return "", false
	}

	if slash := strings.Index(rest, "/"); slash >= 0 {
		rest = rest[:slash]
	}

	return rest, rest != ""
}

// waitOrKill gives a browser that has been asked to close time to leave on its
// own, and kills it if it will not.
//
// The wait matters: a browser that exits normally takes its helper processes
// with it, while a killed one leaves them running — and they go on writing to
// the profile directory, which is how a deleted profile comes back.
//
//nolint:errcheck // the browser is being torn down, so a failed kill or wait leaves nothing to act on.
func (b *browser) waitOrKill(timeout time.Duration) {
	if b.cmd == nil || b.cmd.Process == nil {
		return
	}

	exited := make(chan struct{})
	go func() {
		b.cmd.Process.Wait()
		close(exited)
	}()

	select {
	case <-exited:
	case <-time.After(timeout):
		b.cmd.Process.Kill()
		<-exited
	}
}

// removeProfile deletes the throwaway profile, retrying while a straggling
// helper process writes it back out.
//
//nolint:errcheck // a profile that cannot be removed is left to the temporary directory cleanup.
func (b *browser) removeProfile() {
	if b.profile == "" {
		return
	}

	for attempt := range profileRemovalAttempts {
		os.RemoveAll(b.profile)

		if _, err := os.Stat(b.profile); errors.Is(err, os.ErrNotExist) {
			return
		}

		if attempt < profileRemovalAttempts-1 {
			time.Sleep(profileRemovalDelay)
		}
	}
}
