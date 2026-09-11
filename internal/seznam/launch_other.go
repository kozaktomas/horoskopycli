//go:build !darwin

package seznam

import (
	"context"
	"os/exec"
)

// launchAttempts returns the ways to start the browser, best first. Everywhere
// but macOS there is only one: run it.
func launchAttempts(ctx context.Context, found foundBrowser, profile string, port int) []*exec.Cmd {
	//nolint:gosec // the executable is one the user chose or one of the known browsers.
	return []*exec.Cmd{exec.CommandContext(ctx, found.path, browserArgs(found.engine, profile, port)...)}
}
