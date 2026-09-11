package seznam

import (
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"
)

// newFakeBrowser returns a DevTools connection wired to a stub browser that
// answers every command with reply(request), plus the channel of commands it
// received.
func newFakeBrowser(
	t *testing.T,
	reply func(request map[string]any) string,
) (conn *cdpConn, commands <-chan map[string]any) {
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
			// Never block: a test that polls many times would otherwise
			// fill the channel and stall the stub browser.
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

	return &cdpConn{ws: newWSConn(client)}, requests
}

// answer builds a DevTools reply carrying result for the id of request.
func answer(request map[string]any, result string) string {
	id, ok := request["id"].(float64)
	if !ok {
		id = 0
	}

	body, err := json.Marshal(map[string]any{"id": int(id), "result": json.RawMessage(result)})
	if err != nil {
		return `{"id":0,"result":{}}`
	}

	return string(body)
}

func TestCDPConnCookiesReadsTheJar(t *testing.T) {
	t.Parallel()

	conn, requests := newFakeBrowser(t, func(request map[string]any) string {
		return answer(request, `{"cookies":[
			{"name":"ds","value":"session","domain":".horoskopy.cz","expires":1820610121},
			{"name":"sid","value":"other","domain":".seznam.cz","expires":-1}
		]}`)
	})

	jar, err := conn.cookies()
	if err != nil {
		t.Fatalf("cookies returned %v", err)
	}
	if len(jar) != 2 {
		t.Fatalf("cookies returned %d entries, want 2", len(jar))
	}
	if jar[0].Name != "ds" || jar[0].Value != "session" || jar[0].Domain != ".horoskopy.cz" {
		t.Errorf("first cookie = %+v, want the session cookie", jar[0])
	}

	request := <-requests
	if request["method"] != "Storage.getCookies" {
		t.Errorf("cookies called %v, want Storage.getCookies", request["method"])
	}
}

func TestCDPConnOpenTabAsksForTheURL(t *testing.T) {
	t.Parallel()

	conn, requests := newFakeBrowser(t, func(request map[string]any) string {
		return answer(request, `{"targetId":"abc"}`)
	})

	if err := conn.openTab("https://www.horoskopy.cz/"); err != nil {
		t.Fatalf("openTab returned %v", err)
	}

	request := <-requests
	if request["method"] != "Target.createTarget" {
		t.Fatalf("openTab called %v, want Target.createTarget", request["method"])
	}

	params, ok := request["params"].(map[string]any)
	if !ok {
		t.Fatal("openTab sent no params")
	}
	if params["url"] != "https://www.horoskopy.cz/" {
		t.Errorf("openTab asked for %v, want the horoskopy.cz URL", params["url"])
	}
}

func TestCDPConnSurfacesBrowserErrors(t *testing.T) {
	t.Parallel()

	conn, _ := newFakeBrowser(t, func(request map[string]any) string {
		id, ok := request["id"].(float64)
		if !ok {
			id = 0
		}

		return `{"id":` + strconv.Itoa(int(id)) + `,"error":{"code":-32000,"message":"not allowed"}}`
	})

	_, err := conn.cookies()
	if !errors.Is(err, ErrBrowserProtocol) {
		t.Errorf("cookies against a refusing browser = %v, want ErrBrowserProtocol", err)
	}
}

func TestCDPConnSkipsEvents(t *testing.T) {
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
		writeServerFrame(t, server, wsOpcodeText, true, []byte(`{"method":"Target.targetCreated","params":{}}`))
		writeServerFrame(t, server, wsOpcodeText, true, []byte(`{"id":99,"result":{"cookies":[]}}`))
		writeServerFrame(t, server, wsOpcodeText, true, []byte(`{"id":1,"result":{"cookies":[{"name":"ds"}]}}`))
	}()

	conn := &cdpConn{ws: newWSConn(client)}

	jar, err := conn.cookies()
	if err != nil {
		t.Fatalf("cookies returned %v", err)
	}
	if len(jar) != 1 || jar[0].Name != "ds" {
		t.Errorf("cookies = %+v, want the answer to the command that was sent", jar)
	}
}

func TestCookieExpiresAt(t *testing.T) {
	t.Parallel()

	if got := (cookie{Expires: -1}).expiresAt(); !got.IsZero() {
		t.Errorf("expiresAt of a session cookie = %v, want the zero time", got)
	}

	want := time.Unix(1820610121, 0)
	if got := (cookie{Expires: 1820610121}).expiresAt(); !got.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", got, want)
	}
}
