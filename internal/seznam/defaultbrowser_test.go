package seznam

import "testing"

func TestDesktopExec(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		contents string
		want     string
	}{
		"plain entry": {
			contents: "[Desktop Entry]\nName=Firefox\nExec=firefox %u\nTerminal=false\n",
			want:     "firefox",
		},
		"absolute path": {
			contents: "[Desktop Entry]\nExec=/usr/bin/google-chrome-stable %U\n",
			want:     "/usr/bin/google-chrome-stable",
		},
		"quoted path with a space": {
			contents: "[Desktop Entry]\nExec=\"/opt/My Browser/browser\" --new-window %u\n",
			want:     "/opt/My Browser/browser",
		},
		"actions are ignored": {
			contents: "[Desktop Entry]\nExec=firefox %u\n\n[Desktop Action new-private-window]\nExec=firefox --private-window %u\n",
			want:     "firefox",
		},
		"entry after an action still counts": {
			contents: "[Desktop Action new-window]\nExec=wrong %u\n\n[Desktop Entry]\nExec=right %u\n",
			want:     "right",
		},
		"no exec line": {contents: "[Desktop Entry]\nName=Nothing\n"},
		"empty file":   {contents: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := desktopExec(tt.contents); got != tt.want {
				t.Errorf("desktopExec() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHTTPSHandler(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw  string
		want string
	}{
		"https wins": {
			raw: `{"LSHandlers":[
				{"LSHandlerRoleAll":"com.apple.safari","LSHandlerURLScheme":"http"},
				{"LSHandlerRoleAll":"org.mozilla.firefox","LSHandlerURLScheme":"https"}]}`,
			want: "org.mozilla.firefox",
		},
		"http is the fallback": {
			raw:  `{"LSHandlers":[{"LSHandlerRoleAll":"com.google.Chrome","LSHandlerURLScheme":"http"}]}`,
			want: "com.google.chrome",
		},
		"other handlers are ignored": {
			raw:  `{"LSHandlers":[{"LSHandlerRoleAll":"com.apple.mail","LSHandlerURLScheme":"mailto"}]}`,
			want: "",
		},
		"not JSON": {raw: `<?xml version="1.0"?>`, want: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := httpsHandler([]byte(tt.raw)); got != tt.want {
				t.Errorf("httpsHandler() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegValue(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		output string
		want   string
	}{
		"named value": {
			output: "\r\nHKEY_CURRENT_USER\\Software\\…\\UserChoice\r\n    ProgId    REG_SZ    FirefoxURL-308046B0AF4A39CB\r\n\r\n",
			want:   "FirefoxURL-308046B0AF4A39CB",
		},
		"default value with a command": {
			output: "\r\nHKEY_CLASSES_ROOT\\FirefoxURL\\shell\\open\\command\r\n    (Default)    REG_SZ    \"C:\\Program Files\\Mozilla Firefox\\firefox.exe\" -osint -url \"%1\"\r\n",
			want:   "\"C:\\Program Files\\Mozilla Firefox\\firefox.exe\" -osint -url \"%1\"",
		},
		"nothing there": {output: "ERROR: The system was unable to find the specified registry key or value."},
		"empty":         {output: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := regValue(tt.output); got != tt.want {
				t.Errorf("regValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommandExecutable(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		`"C:\Program Files\Mozilla Firefox\firefox.exe" -osint -url "%1"`:              `C:\Program Files\Mozilla Firefox\firefox.exe`,
		`"C:\Program Files\Google\Chrome\Application\chrome.exe" --single-argument %1`: `C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\browser.exe %1`: `C:\browser.exe`,
		``:                  ``,
	}

	for command, want := range tests {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			if got := commandExecutable(command); got != want {
				t.Errorf("commandExecutable(%q) = %q, want %q", command, got, want)
			}
		})
	}
}
