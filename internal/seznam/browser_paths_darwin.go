package seznam

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// bundleExecutables maps the bundle identifier macOS records as the handler
// for https to the executable inside the application.
//
// Safari is deliberately absent: it exposes neither the DevTools protocol nor
// WebDriver BiDi, so a Safari user falls through to whatever else is
// installed.
var bundleExecutables = map[string]string{
	"org.mozilla.firefox":                 "/Applications/Firefox.app/Contents/MacOS/firefox",
	"org.mozilla.firefoxdeveloperedition": "/Applications/Firefox Developer Edition.app/Contents/MacOS/firefox",
	"org.mozilla.nightly":                 "/Applications/Firefox Nightly.app/Contents/MacOS/firefox",
	"app.zen-browser.zen":                 "/Applications/Zen.app/Contents/MacOS/zen",
	"com.google.chrome":                   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"org.chromium.chromium":               "/Applications/Chromium.app/Contents/MacOS/Chromium",
	"com.brave.browser":                   "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	"com.microsoft.edgemac":               "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	"com.vivaldi.vivaldi":                 "/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
	"com.operasoftware.opera":             "/Applications/Opera.app/Contents/MacOS/Opera",
}

// launchServicesPreferences is where macOS records which application opens
// which kind of link.
const launchServicesPreferences = "Library/Preferences/com.apple.LaunchServices/com.apple.launchservices.secure.plist"

// installedBrowserPaths are the usual places a browser is installed, tried
// when neither the default browser nor PATH turns one up.
func installedBrowserPaths() []string {
	paths := make([]string, 0, len(bundleExecutables))
	for _, path := range bundleExecutables {
		if path != "" {
			paths = append(paths, path)
		}
	}

	return paths
}

// defaultBrowserPath reads the LaunchServices preferences and maps the https
// handler to the executable inside its application bundle.
func defaultBrowserPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not locate the home directory: %w", err)
	}

	plist := filepath.Join(home, launchServicesPreferences)

	raw, err := exec.Command("plutil", "-convert", "json", "-o", "-", plist).Output()
	if err != nil {
		return "", fmt.Errorf("could not read the default browser: %w", err)
	}

	bundle := httpsHandler(raw)
	executable, known := bundleExecutables[bundle]
	if !known || executable == "" {
		return "", fmt.Errorf("%w: %q is not a browser this CLI can drive", ErrNoBrowser, bundle)
	}

	if _, err := os.Stat(executable); err != nil {
		return "", fmt.Errorf("%w: %s is not installed where expected", ErrNoBrowser, bundle)
	}

	return executable, nil
}
