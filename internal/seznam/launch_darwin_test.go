package seznam

import "testing"

func TestApplicationBundle(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		executable string
		want       string
		wantOK     bool
	}{
		"firefox": {
			executable: "/Applications/Firefox.app/Contents/MacOS/firefox",
			want:       "/Applications/Firefox.app",
			wantOK:     true,
		},
		"chrome with a space": {
			executable: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			want:       "/Applications/Google Chrome.app",
			wantOK:     true,
		},
		"somewhere else entirely": {
			executable: "/Users/someone/Applications/Firefox.app/Contents/MacOS/firefox",
			want:       "/Users/someone/Applications/Firefox.app",
			wantOK:     true,
		},
		"not in a bundle": {executable: "/opt/homebrew/bin/firefox"},
		"short path":      {executable: "/firefox"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := applicationBundle(tt.executable)
			if ok != tt.wantOK {
				t.Fatalf("applicationBundle(%q) ok = %v, want %v", tt.executable, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("applicationBundle(%q) = %q, want %q", tt.executable, got, tt.want)
			}
		})
	}
}

func TestLaunchAttemptsPrefersOpeningTheBundle(t *testing.T) {
	t.Parallel()

	attempts := launchAttempts(
		t.Context(),
		foundBrowser{path: "/Applications/Firefox.app/Contents/MacOS/firefox", engine: engineFirefox},
		"/profile",
		45999,
	)

	if len(attempts) != 2 {
		t.Fatalf("launchAttempts returned %d ways to start the browser, want the bundle and the executable", len(attempts))
	}

	// `open -n` is what gives a second instance a window of its own on macOS.
	if got := attempts[0].Args[0]; got != "open" {
		t.Errorf("first attempt runs %q, want open", got)
	}
	if attempts[1].Path != "/Applications/Firefox.app/Contents/MacOS/firefox" {
		t.Errorf("second attempt runs %q, want the executable itself", attempts[1].Path)
	}
}

func TestLaunchAttemptsRunsAPlainExecutableDirectly(t *testing.T) {
	t.Parallel()

	attempts := launchAttempts(
		t.Context(),
		foundBrowser{path: "/opt/homebrew/bin/firefox", engine: engineFirefox},
		"/profile",
		45999,
	)

	if len(attempts) != 1 {
		t.Fatalf("launchAttempts returned %d ways to start a browser outside a bundle, want 1", len(attempts))
	}
}
