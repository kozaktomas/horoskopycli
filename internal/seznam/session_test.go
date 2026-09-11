package seznam

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateConfigDir points the user configuration directory at a temporary one,
// so tests never touch a real session file.
func isolateConfigDir(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux and the BSDs
	t.Setenv("HOME", dir)            // macOS, which ignores XDG
	t.Setenv(EnvCookie, "")
}

func TestSessionSaveAndLoad(t *testing.T) {
	isolateConfigDir(t)

	want := Session{
		Cookie:  "cookie-value",
		Account: "panbotka@seznam.cz",
		Expires: time.Date(2027, 9, 10, 21, 2, 1, 0, time.UTC),
	}

	if err := want.Save(); err != nil {
		t.Fatalf("Save returned %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got.Cookie != want.Cookie || got.Account != want.Account || !got.Expires.Equal(want.Expires) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestSessionSaveKeepsTheCookiePrivate(t *testing.T) {
	isolateConfigDir(t)

	if err := (Session{Cookie: "cookie-value"}).Save(); err != nil {
		t.Fatalf("Save returned %v", err)
	}

	path, err := SessionPath()
	if err != nil {
		t.Fatalf("SessionPath returned %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("could not stat the session file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != sessionFilePerm {
		t.Errorf("session file mode = %o, want %o", perm, sessionFilePerm)
	}
}

func TestLoadWithoutAStoredSession(t *testing.T) {
	isolateConfigDir(t)

	if _, err := Load(); !errors.Is(err, ErrNoSession) {
		t.Errorf("Load without a session = %v, want ErrNoSession", err)
	}
}

func TestLoadPrefersTheEnvironment(t *testing.T) {
	isolateConfigDir(t)

	if err := (Session{Cookie: "from-file"}).Save(); err != nil {
		t.Fatalf("Save returned %v", err)
	}
	t.Setenv(EnvCookie, "from-environment")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got.Cookie != "from-environment" {
		t.Errorf("Load() cookie = %q, want the one from %s", got.Cookie, EnvCookie)
	}
}

func TestLoadRejectsUnusableFiles(t *testing.T) {
	tests := map[string]struct {
		contents string
		wantErr  error
	}{
		"empty cookie": {contents: `{"cookie":""}`, wantErr: ErrNoSession},
		"not JSON":     {contents: `nonsense`},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			isolateConfigDir(t)

			path, err := SessionPath()
			if err != nil {
				t.Fatalf("SessionPath returned %v", err)
			}
			if err := os.MkdirAll(filepath.Dir(path), sessionDirPerm); err != nil {
				t.Fatalf("could not create the config directory: %v", err)
			}
			if err := os.WriteFile(path, []byte(tt.contents), sessionFilePerm); err != nil {
				t.Fatalf("could not write the session file: %v", err)
			}

			_, err = Load()
			if err == nil {
				t.Fatal("Load should have failed")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Load() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestForgetRemovesTheSession(t *testing.T) {
	isolateConfigDir(t)

	if err := (Session{Cookie: "cookie-value"}).Save(); err != nil {
		t.Fatalf("Save returned %v", err)
	}
	if err := Forget(); err != nil {
		t.Fatalf("Forget returned %v", err)
	}
	if _, err := Load(); !errors.Is(err, ErrNoSession) {
		t.Errorf("Load after Forget = %v, want ErrNoSession", err)
	}
}

func TestForgetWithoutASessionSucceeds(t *testing.T) {
	isolateConfigDir(t)

	if err := Forget(); err != nil {
		t.Errorf("Forget without a session returned %v", err)
	}
}

func TestSessionPathNamesTheCLI(t *testing.T) {
	isolateConfigDir(t)

	path, err := SessionPath()
	if err != nil {
		t.Fatalf("SessionPath returned %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(path), "horoskopycli/session.json") {
		t.Errorf("SessionPath() = %q, want it to end in horoskopycli/session.json", path)
	}
}
