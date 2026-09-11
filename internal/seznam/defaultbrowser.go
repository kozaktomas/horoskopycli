package seznam

import (
	"encoding/json"
	"strings"
)

// Finding the user's default browser is a different job on every platform, so
// the parts that talk to the system live in the per-platform files next to
// this one. What can be parsed without touching the system lives here, where
// it can be tested anywhere.

// desktopExec returns the executable a freedesktop .desktop file launches, or
// an empty string when the file names none.
//
// Only the [Desktop Entry] section counts: actions further down the file have
// Exec lines of their own, and one of those would open the wrong window.
func desktopExec(contents string) string {
	inEntry := false

	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"

			continue
		}

		if !inEntry || !strings.HasPrefix(line, "Exec=") {
			continue
		}

		return execExecutable(strings.TrimPrefix(line, "Exec="))
	}

	return ""
}

// execExecutable takes the program out of a command line, dropping arguments
// and the %u and %U placeholders a .desktop file passes a URL in.
func execExecutable(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}

	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return command[1 : end+1]
		}
	}

	return strings.Fields(command)[0]
}

// launchServices is the part of the macOS handler list this cares about.
type launchServices struct {
	Handlers []struct {
		RoleAll   string `json:"LSHandlerRoleAll"`
		URLScheme string `json:"LSHandlerURLScheme"`
	} `json:"LSHandlers"`
}

// httpsHandler returns the bundle identifier macOS opens https links with,
// given the LaunchServices preferences converted to JSON.
func httpsHandler(raw []byte) string {
	var preferences launchServices
	if err := json.Unmarshal(raw, &preferences); err != nil {
		return ""
	}

	fallback := ""
	for _, handler := range preferences.Handlers {
		switch strings.ToLower(handler.URLScheme) {
		case "https":
			return strings.ToLower(handler.RoleAll)
		case "http":
			fallback = strings.ToLower(handler.RoleAll)
		}
	}

	return fallback
}

// regValue returns the data of the single value in `reg query` output.
func regValue(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "REG_SZ", 2)
		if len(fields) != 2 {
			continue
		}

		if value := strings.TrimSpace(fields[1]); value != "" {
			return value
		}
	}

	return ""
}

// commandExecutable takes the program out of a Windows shell open command,
// which quotes the path and appends arguments such as -osint -url "%1".
func commandExecutable(command string) string {
	return execExecutable(command)
}
