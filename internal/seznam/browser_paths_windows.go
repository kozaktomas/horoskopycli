package seznam

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// urlAssociation is where Windows records the program identifier that opens
// https links.
const urlAssociation = `HKCU\Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`

// browserSubPaths are the usual install locations, relative to one of the
// program directories below.
var browserSubPaths = []string{
	`Mozilla Firefox\firefox.exe`,
	`Google\Chrome\Application\chrome.exe`,
	`Microsoft\Edge\Application\msedge.exe`,
	`BraveSoftware\Brave-Browser\Application\brave.exe`,
	`Chromium\Application\chrome.exe`,
	`Vivaldi\Application\vivaldi.exe`,
}

// programDirectories are the roots those install locations hang off, covering
// both machine-wide and per-user installations.
func programDirectories() []string {
	roots := make([]string, 0, 4)
	for _, variable := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if dir := os.Getenv(variable); dir != "" {
			roots = append(roots, dir)
		}
	}

	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Join(local, "Programs"))
	}

	return roots
}

// installedBrowserPaths are the usual places a browser is installed, tried
// when neither the default browser nor PATH turns one up.
func installedBrowserPaths() []string {
	paths := make([]string, 0, len(browserSubPaths)*4)
	for _, root := range programDirectories() {
		for _, sub := range browserSubPaths {
			paths = append(paths, filepath.Join(root, sub))
		}
	}

	return paths
}

// defaultBrowserPath reads the program registered for https out of the
// registry and resolves it to an executable.
func defaultBrowserPath() (string, error) {
	progID := regValue(runReg(urlAssociation, "/v", "ProgId"))
	if progID == "" {
		return "", fmt.Errorf("%w: no program is registered for https", ErrNoBrowser)
	}

	command := regValue(runReg(`HKCR\`+progID+`\shell\open\command`, "/ve"))
	executable := commandExecutable(command)
	if executable == "" {
		return "", fmt.Errorf("%w: %s names no command", ErrNoBrowser, progID)
	}

	if _, err := os.Stat(executable); err != nil {
		return "", fmt.Errorf("%w: %s is not where the registry says", ErrNoBrowser, progID)
	}

	return executable, nil
}

// runReg queries one registry key, returning empty output on any failure.
func runReg(key string, args ...string) string {
	out, err := exec.Command("reg", append([]string{"query", key}, args...)...).Output()
	if err != nil {
		return ""
	}

	return string(out)
}
