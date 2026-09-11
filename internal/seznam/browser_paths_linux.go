package seznam

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// desktopDirectories are where a .desktop file may live, including the ones
// snap and flatpak install into.
var desktopDirectories = []string{
	"~/.local/share/applications",
	"/usr/local/share/applications",
	"/usr/share/applications",
	"/var/lib/snapd/desktop/applications",
	"/var/lib/flatpak/exports/share/applications",
	"~/.local/share/flatpak/exports/share/applications",
}

// installedBrowserPaths are the browsers worth looking for outside PATH. On
// Linux everything of interest is in PATH already, so this is empty.
func installedBrowserPaths() []string {
	return nil
}

// defaultBrowserPath asks the desktop environment which browser opens links,
// and resolves its .desktop file to an executable.
func defaultBrowserPath() (string, error) {
	out, err := exec.Command("xdg-settings", "get", "default-web-browser").Output()
	if err != nil {
		return "", fmt.Errorf("could not ask for the default browser: %w", err)
	}

	entry := strings.TrimSpace(string(out))
	if entry == "" {
		return "", ErrNoBrowser
	}

	for _, dir := range desktopDirectories {
		contents, err := os.ReadFile(filepath.Join(expandHome(dir), entry)) //nolint:gosec // a desktop entry name from xdg-settings.
		if err != nil {
			continue
		}

		executable := desktopExec(string(contents))
		if executable == "" {
			continue
		}

		if path, err := exec.LookPath(executable); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("%w: %s names no usable browser", ErrNoBrowser, entry)
}

// expandHome resolves a leading ~ in a directory name.
func expandHome(dir string) string {
	rest, found := strings.CutPrefix(dir, "~/")
	if !found {
		return dir
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}

	return filepath.Join(home, rest)
}
