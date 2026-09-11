//go:build !linux && !darwin && !windows

package seznam

// installedBrowserPaths are the usual places a browser is installed. Nothing
// is known about this platform, so PATH is all there is.
func installedBrowserPaths() []string {
	return nil
}

// defaultBrowserPath is not implemented here, so the search falls back to
// looking for a known browser in PATH.
func defaultBrowserPath() (string, error) {
	return "", ErrNoBrowser
}
