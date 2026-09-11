package seznam

import (
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
)

// newFakeFirefox returns a BiDi session wired to a stub browser that answers
// every command with reply(request), plus the channel of commands it received.
func newFakeFirefox(
	t *testing.T,
	reply func(request map[string]any) string,
) (conn *bidiConn, commands <-chan map[string]any) {
	t.Helper()

	client, server := net.Pipe()
	requests := make(chan map[string]any, 8)

	go func() {
		defer close(requests)

		for {
			payload, err := readMaskedFrame(server)
			if err != nil {
				return
			}

			var request map[string]any
			if err := json.Unmarshal(payload, &request); err != nil {
				t.Errorf("browser received invalid JSON: %v", err)

				return
			}

			select {
			case requests <- request:
			default:
			}

			writeServerFrame(t, server, wsOpcodeText, true, []byte(reply(request)))
		}
	}()

	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	return &bidiConn{ws: newWSConn(client)}, requests
}

// bidiSuccess builds a successful BiDi answer for the id of request.
func bidiSuccess(request map[string]any, result string) string {
	id, ok := request["id"].(float64)
	if !ok {
		id = 0
	}

	return `{"type":"success","id":` + strconv.Itoa(int(id)) + `,"result":` + result + `}`
}

func TestBiDiEndpoint(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"ws://127.0.0.1:43227":          "ws://127.0.0.1:43227/session",
		"ws://127.0.0.1:43227/":         "ws://127.0.0.1:43227/",
		"ws://127.0.0.1:43227/session":  "ws://127.0.0.1:43227/session",
		"ws://localhost:9222/devtools/": "ws://localhost:9222/devtools/",
	}

	for announced, want := range tests {
		t.Run(announced, func(t *testing.T) {
			t.Parallel()

			if got := bidiEndpoint(announced); got != want {
				t.Errorf("bidiEndpoint(%q) = %q, want %q", announced, got, want)
			}
		})
	}
}

func TestBiDiConnCookiesUnwrapsValues(t *testing.T) {
	t.Parallel()

	conn, requests := newFakeFirefox(t, func(request map[string]any) string {
		return bidiSuccess(request, `{"cookies":[
			{"name":"ds","domain":".horoskopy.cz","expiry":1820610121,
			 "httpOnly":true,"value":{"type":"string","value":"session"}},
			{"name":"binary","domain":".horoskopy.cz",
			 "value":{"type":"base64","value":"AAECAw=="}}
		]}`)
	})

	jar, err := conn.cookies()
	if err != nil {
		t.Fatalf("cookies returned %v", err)
	}

	// The base64 cookie is dropped: no session of ours looks like that.
	if len(jar) != 1 {
		t.Fatalf("cookies returned %d entries, want 1", len(jar))
	}
	if jar[0].Name != "ds" || jar[0].Value != "session" || jar[0].Domain != ".horoskopy.cz" {
		t.Errorf("cookie = %+v, want the unwrapped session cookie", jar[0])
	}
	if jar[0].expiresAt().Unix() != 1820610121 {
		t.Errorf("cookie expiry = %v, want the BiDi expiry field", jar[0].expiresAt())
	}

	request := <-requests
	if request["method"] != "storage.getCookies" {
		t.Errorf("cookies called %v, want storage.getCookies", request["method"])
	}
	// Filtering happens locally: the BiDi domain filter matches exactly, so
	// asking for horoskopy.cz would miss a cookie stored as .horoskopy.cz.
	params, ok := request["params"].(map[string]any)
	if !ok {
		t.Fatal("cookies sent no params object")
	}
	if _, filtered := params["filter"]; filtered {
		t.Error("cookies should ask for the whole jar, not a filtered one")
	}
}

func TestBiDiConnOpenTabCreatesAndNavigates(t *testing.T) {
	t.Parallel()

	conn, requests := newFakeFirefox(t, func(request map[string]any) string {
		return bidiSuccess(request, `{"context":"abc"}`)
	})

	if err := conn.openTab("https://www.horoskopy.cz/"); err != nil {
		t.Fatalf("openTab returned %v", err)
	}

	created := <-requests
	if created["method"] != "browsingContext.create" {
		t.Fatalf("openTab called %v first, want browsingContext.create", created["method"])
	}

	// Creating a tab takes no address — BiDi ignores one rather than refusing
	// it, which would leave the tab blank and the login waiting forever.
	navigated := <-requests
	if navigated["method"] != "browsingContext.navigate" {
		t.Fatalf("openTab called %v second, want browsingContext.navigate", navigated["method"])
	}

	params, ok := navigated["params"].(map[string]any)
	if !ok {
		t.Fatal("navigate sent no params")
	}
	if params["url"] != "https://www.horoskopy.cz/" {
		t.Errorf("navigate asked for %v, want the horoskopy.cz URL", params["url"])
	}
	if params["context"] != "abc" {
		t.Errorf("navigate used context %v, want the tab that was just created", params["context"])
	}
}

func TestBiDiConnCloseBrowserAsksFirefoxToLeave(t *testing.T) {
	t.Parallel()

	conn, requests := newFakeFirefox(t, func(request map[string]any) string {
		return bidiSuccess(request, `{}`)
	})

	if err := conn.closeBrowser(); err != nil {
		t.Fatalf("closeBrowser returned %v", err)
	}

	if request := <-requests; request["method"] != "browser.close" {
		t.Errorf("closeBrowser called %v, want browser.close", request["method"])
	}
}

func TestBiDiConnSurfacesErrors(t *testing.T) {
	t.Parallel()

	conn, _ := newFakeFirefox(t, func(request map[string]any) string {
		id, ok := request["id"].(float64)
		if !ok {
			id = 0
		}

		return `{"type":"error","id":` + strconv.Itoa(int(id)) +
			`,"error":"unknown command","message":"storage.getCookies is not supported"}`
	})

	_, err := conn.cookies()
	if !errors.Is(err, ErrBiDiProtocol) {
		t.Fatalf("cookies against a refusing browser = %v, want ErrBiDiProtocol", err)
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %q, want it to carry the browser's message", err)
	}
}

func TestBiDiConnSkipsEvents(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	go func() {
		if _, err := readMaskedFrame(server); err != nil {
			return
		}
		writeServerFrame(t, server, wsOpcodeText, true,
			[]byte(`{"type":"event","method":"log.entryAdded","params":{}}`))
		writeServerFrame(t, server, wsOpcodeText, true,
			[]byte(`{"type":"success","id":1,"result":{"cookies":[{"name":"ds","domain":".horoskopy.cz","value":{"type":"string","value":"v"}}]}}`))
	}()

	conn := &bidiConn{ws: newWSConn(client)}

	jar, err := conn.cookies()
	if err != nil {
		t.Fatalf("cookies returned %v", err)
	}
	if len(jar) != 1 {
		t.Errorf("cookies = %+v, want the answer rather than the event", jar)
	}
}
