package seznam

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// launchAttempts returns the ways to start the browser, best first.
//
// On macOS a browser is an application bundle, and running the executable
// inside it directly gives a process without a window when another instance of
// the same bundle is already running — which it usually is. `open -n` is the
// documented way to ask for a genuinely separate instance, and the one that
// comes with a window. Running the executable is kept as a fallback, for a
// browser that is not in a bundle at all.
func launchAttempts(ctx context.Context, found foundBrowser, profile string, port int) []*exec.Cmd {
	args := browserArgs(found.engine, profile, port)

	attempts := make([]*exec.Cmd, 0, 2)
	if bundle, ok := applicationBundle(found.path); ok {
		//nolint:gosec // the bundle is derived from a browser path the user chose.
		attempts = append(attempts, exec.CommandContext(ctx, "open", append([]string{"-n", "-a", bundle, "--args"}, args...)...))
	}

	//nolint:gosec // the executable is one the user chose or one of the known browsers.
	attempts = append(attempts, exec.CommandContext(ctx, found.path, args...))

	return attempts
}

// applicationBundle walks up from an executable inside Contents/MacOS to the
// .app directory holding it.
func applicationBundle(executable string) (string, bool) {
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(executable)))
	if !strings.HasSuffix(bundle, ".app") {
		return "", false
	}

	return bundle, true
}
