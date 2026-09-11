package seznam

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stubExecutable writes an executable file with the given name into dir.
func stubExecutable(t *testing.T, dir, name string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("could not create %s: %v", path, err)
	}

	return path
}

func TestEngineFor(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path          string
		want          engine
		wantSupported bool
	}{
		"firefox":            {path: "/usr/bin/firefox", want: engineFirefox, wantSupported: true},
		"firefox on windows": {path: `C:\Program Files\Mozilla Firefox\firefox.exe`, want: engineFirefox, wantSupported: true},
		"firefox on macos":   {path: "/Applications/Firefox.app/Contents/MacOS/firefox", want: engineFirefox, wantSupported: true},
		"librewolf":          {path: "/usr/bin/librewolf", want: engineFirefox, wantSupported: true},
		"chrome":             {path: "/opt/google/chrome/google-chrome", want: engineChromium, wantSupported: true},
		"chromium":           {path: "/usr/bin/chromium-browser", want: engineChromium, wantSupported: true},
		"brave":              {path: "/usr/bin/brave-browser", want: engineChromium, wantSupported: true},
		"edge":               {path: "/usr/bin/microsoft-edge", want: engineChromium, wantSupported: true},
		"safari":             {path: "/Applications/Safari.app/Contents/MacOS/Safari", wantSupported: false},
		"something else":     {path: "/usr/bin/epiphany", wantSupported: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, supported := engineFor(tt.path)
			if supported != tt.wantSupported {
				t.Fatalf("engineFor(%q) supported = %v, want %v", tt.path, supported, tt.wantSupported)
			}
			if supported && got != tt.want {
				t.Errorf("engineFor(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFindBrowserPrefersTheRequestedOne(t *testing.T) {
	dir := t.TempDir()
	wanted := stubExecutable(t, dir, "firefox")
	stubExecutable(t, dir, "chromium")
	t.Setenv("PATH", dir)

	got, err := findBrowser("firefox")
	if err != nil {
		t.Fatalf("findBrowser returned %v", err)
	}
	if got.path != wanted {
		t.Errorf("findBrowser(%q) = %q, want %q", "firefox", got.path, wanted)
	}
	if got.engine != engineFirefox {
		t.Errorf("findBrowser(%q) engine = %v, want Firefox", "firefox", got.engine)
	}
}

func TestFindBrowserSearchesPath(t *testing.T) {
	dir := t.TempDir()
	wanted := stubExecutable(t, dir, "chromium")
	t.Setenv("PATH", dir)

	got, err := findBrowser("")
	if err != nil {
		t.Fatalf("findBrowser returned %v", err)
	}
	if got.path != wanted {
		t.Errorf("findBrowser() = %q, want %q", got.path, wanted)
	}
	if got.engine != engineChromium {
		t.Errorf("findBrowser() engine = %v, want Chromium", got.engine)
	}
}

func TestFindBrowserReportsAMissingBrowser(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if _, err := findBrowser(""); !errors.Is(err, ErrNoBrowser) {
		// A machine with a browser installed in a standard location still
		// finds one, which is a pass as far as this function is concerned.
		if err == nil {
			t.Skip("this machine has a browser in a well-known location")
		}

		t.Errorf("findBrowser() = %v, want ErrNoBrowser", err)
	}
}

func TestFindBrowserRejectsAnUnusableRequest(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if _, err := findBrowser("definitely-not-a-browser"); err == nil {
		t.Error("findBrowser with a missing executable should fail")
	}
}

func TestBrowserArgs(t *testing.T) {
	t.Parallel()

	firefox := strings.Join(browserArgs(engineFirefox, "/profile", 45999), " ")
	for _, want := range []string{"--profile /profile", "--no-remote", "--remote-debugging-port=45999"} {
		if !strings.Contains(firefox, want) {
			t.Errorf("Firefox args %q should contain %q", firefox, want)
		}
	}
	if strings.Contains(firefox, "--user-data-dir") {
		t.Error("Firefox does not understand --user-data-dir")
	}

	chromium := strings.Join(browserArgs(engineChromium, "/profile", 45999), " ")
	for _, want := range []string{"--user-data-dir=/profile", "--remote-debugging-port=45999"} {
		if !strings.Contains(chromium, want) {
			t.Errorf("Chromium args %q should contain %q", chromium, want)
		}
	}
	if strings.Contains(chromium, "--profile ") {
		t.Error("Chromium does not understand --profile")
	}

	// The address is opened over the protocol instead, so that a browser which
	// is already running cannot pick it up.
	for _, args := range []string{firefox, chromium} {
		if strings.Contains(args, "http") {
			t.Errorf("args %q should carry no address", args)
		}
	}
}

func TestSnapProfileDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, ok := snapProfileDir("/usr/bin/firefox"); ok {
		t.Error("snapProfileDir should report nothing without a snap directory")
	}

	snap := filepath.Join(home, "snap", "firefox", "common")
	if err := os.MkdirAll(snap, 0o700); err != nil {
		t.Fatalf("could not create the snap directory: %v", err)
	}

	got, ok := snapProfileDir("/usr/bin/firefox")
	if !ok {
		t.Fatal("snapProfileDir should find the snap directory")
	}
	if got != snap {
		t.Errorf("snapProfileDir() = %q, want %q", got, snap)
	}

	// A browser that is not that snap keeps the ordinary temporary directory.
	if _, ok := snapProfileDir("/opt/google/chrome/google-chrome"); ok {
		t.Error("snapProfileDir should not send Chrome into the Firefox snap")
	}
}

func TestNewProfileUsesTheSnapDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	snap := filepath.Join(home, "snap", "firefox", "common")
	if err := os.MkdirAll(snap, 0o700); err != nil {
		t.Fatalf("could not create the snap directory: %v", err)
	}

	profile, err := newProfile(foundBrowser{path: "/snap/bin/firefox", engine: engineFirefox})
	if err != nil {
		t.Fatalf("newProfile returned %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(profile)
	})

	if filepath.Dir(profile) != snap {
		t.Errorf("newProfile() = %q, want it inside %q: a snap cannot read /tmp", profile, snap)
	}
}

func TestFreePortReturnsSomethingUsable(t *testing.T) {
	t.Parallel()

	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort returned %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("freePort() = %d, want a usable port", port)
	}

	// The port has to be free again by the time it is returned, or the browser
	// could not bind it.
	listener, err := net.Listen("tcp", localAddress(port))
	if err != nil {
		t.Fatalf("the port freePort chose is still taken: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Errorf("could not close the test listener: %v", err)
	}
}

func TestProbeEndpointFindsFirefoxByItsPort(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not listen: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("listener has no TCP address")
	}

	endpoint, found := probeEndpoint(engineFirefox, address.Port)
	if !found {
		t.Fatal("probeEndpoint should find a listening Firefox")
	}
	if !strings.HasSuffix(endpoint, "/session") {
		t.Errorf("probeEndpoint() = %q, want the BiDi session path", endpoint)
	}
}

func TestProbeEndpointAsksChromiumWhereItIs(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Errorf("browser was asked for %q, want /json/version", r.URL.Path)
		}

		if _, err := w.Write([]byte(`{"webSocketDebuggerUrl":"ws://127.0.0.1:1/devtools/browser/abc"}`)); err != nil {
			t.Errorf("could not answer: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatalf("could not read the test server port: %v", err)
	}

	endpoint, found := probeEndpoint(engineChromium, port)
	if !found {
		t.Fatal("probeEndpoint should read the browser's own description")
	}
	if endpoint != "ws://127.0.0.1:1/devtools/browser/abc" {
		t.Errorf("probeEndpoint() = %q, want the address the browser named", endpoint)
	}
}

func TestProbeEndpointReportsNothingOnADeadPort(t *testing.T) {
	t.Parallel()

	for name, spoken := range map[string]engine{"firefox": engineFirefox, "chromium": engineChromium} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, found := probeEndpoint(spoken, 1); found {
				t.Error("probeEndpoint should find nothing on a port without a browser")
			}
		})
	}
}

func TestWaitForEndpointGivesUp(t *testing.T) {
	t.Parallel()

	_, err := waitForEndpoint(engineFirefox, 1, 200*time.Millisecond)
	if !errors.Is(err, ErrNoDebugPort) {
		t.Errorf("waitForEndpoint with no browser = %v, want ErrNoDebugPort", err)
	}
}

func TestWaitForEndpointFindsALateBrowser(t *testing.T) {
	t.Parallel()

	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort returned %v", err)
	}

	go func() {
		time.Sleep(150 * time.Millisecond)

		listener, err := net.Listen("tcp", localAddress(port))
		if err != nil {
			return // the port was taken in the meantime; the test still passes or times out
		}
		time.Sleep(2 * time.Second)
		_ = listener.Close()
	}()

	endpoint, err := waitForEndpoint(engineFirefox, port, 5*time.Second)
	if err != nil {
		t.Fatalf("waitForEndpoint returned %v", err)
	}
	if endpoint != fmt.Sprintf("ws://127.0.0.1:%d/session", port) {
		t.Errorf("waitForEndpoint() = %q", endpoint)
	}
}

func TestBrowserWaitOrKillEndsAStubbornBrowser(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start a stand-in for a browser: %v", err)
	}

	started := time.Now()
	(&browser{cmd: cmd}).waitOrKill(50 * time.Millisecond)

	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("waitOrKill took %s, want it to give up quickly", elapsed)
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("waitOrKill should have killed a browser that would not leave")
	}
}

func TestBrowserWaitOrKillReturnsWhenTheBrowserLeaves(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start a stand-in for a browser: %v", err)
	}

	started := time.Now()
	(&browser{cmd: cmd}).waitOrKill(10 * time.Second)

	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("waitOrKill waited %s for a browser that had already left", elapsed)
	}
}

func TestBrowserStopIsSafeWithoutAProcess(t *testing.T) {
	t.Parallel()

	profile := t.TempDir()
	(&browser{profile: profile}).stop()

	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stop should remove the throwaway profile, stat returned %v", err)
	}
}

func TestEndpointAddress(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint string
		want     string
		wantOK   bool
	}{
		"chromium endpoint": {
			endpoint: "ws://127.0.0.1:45037/devtools/browser/c9fa5370",
			want:     "127.0.0.1:45037",
			wantOK:   true,
		},
		"firefox endpoint": {endpoint: "ws://127.0.0.1:43227", want: "127.0.0.1:43227", wantOK: true},
		"with a session path": {
			endpoint: "ws://127.0.0.1:43227/session",
			want:     "127.0.0.1:43227",
			wantOK:   true,
		},
		"not a websocket": {endpoint: "http://127.0.0.1:43227"},
		"empty":           {endpoint: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := endpointAddress(tt.endpoint)
			if ok != tt.wantOK {
				t.Fatalf("endpointAddress(%q) ok = %v, want %v", tt.endpoint, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("endpointAddress(%q) = %q, want %q", tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestBrowserWaitForPortReturnsWhenNothingListens(t *testing.T) {
	t.Parallel()

	started := time.Now()
	(&browser{endpoint: "ws://127.0.0.1:0/devtools"}).waitForPort(5 * time.Second)

	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("waitForPort took %s against a dead port, want it to return at once", elapsed)
	}
}

func TestBrowserWaitForPortGivesUpOnALiveBrowser(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not listen: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	started := time.Now()
	(&browser{endpoint: "ws://" + listener.Addr().String() + "/devtools"}).waitForPort(300 * time.Millisecond)

	if elapsed := time.Since(started); elapsed < 300*time.Millisecond {
		t.Errorf("waitForPort returned after %s, want it to wait out its timeout", elapsed)
	}
}
