package seznam

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Firefox does not speak the DevTools protocol; it speaks WebDriver BiDi. The
// shape is the same — a command in, an answer with the same id out — so this
// mirrors cdp.go command for command, and both satisfy cookieSource.

// bidiSessionPath is where the socket lives. Firefox announces the server as a
// bare ws://host:port, and the session endpoint hangs off it.
const bidiSessionPath = "/session"

// ErrBiDiProtocol is returned when Firefox answers a command with an error or
// with something unparseable.
var ErrBiDiProtocol = errors.New("browser protocol error")

// bidiConn is a WebDriver BiDi session with a browser.
type bidiConn struct {
	ws     *wsConn
	lastID int
}

// bidiEndpoint completes the address Firefox announces, which names the
// server but not the session socket hanging off it.
func bidiEndpoint(announced string) string {
	if strings.Contains(strings.TrimPrefix(announced, "ws://"), "/") {
		return announced
	}

	return announced + bidiSessionPath
}

// dialBiDi connects to a Firefox remote agent and opens a session on it.
func dialBiDi(endpoint string) (*bidiConn, error) {
	ws, err := wsDial(bidiEndpoint(endpoint), cdpDialTimeout)
	if err != nil {
		return nil, err
	}

	conn := &bidiConn{ws: ws}
	if err := conn.call("session.new", map[string]any{"capabilities": map[string]any{}}, nil); err != nil {
		_ = conn.close()

		return nil, err
	}

	return conn, nil
}

// bidiResponse is the envelope every BiDi answer arrives in. Events carry no
// id, and errors describe themselves in the top-level fields.
type bidiResponse struct {
	ID      *int            `json:"id"`
	Type    string          `json:"type"`
	Result  json.RawMessage `json:"result"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

// call sends a BiDi command and decodes its result into out, which may be nil
// when the answer is not needed.
func (c *bidiConn) call(method string, params map[string]any, out any) error {
	c.lastID++
	id := c.lastID

	if params == nil {
		params = map[string]any{}
	}

	request, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("could not encode %s command: %w", method, err)
	}

	if err := c.ws.writeText(request); err != nil {
		return fmt.Errorf("could not send %s command: %w", method, err)
	}

	return c.readResponse(id, method, out)
}

// readResponse waits for the answer to the command with the given id.
func (c *bidiConn) readResponse(id int, method string, out any) error {
	for {
		raw, err := c.ws.readText()
		if err != nil {
			return fmt.Errorf("could not read answer to %s: %w", method, err)
		}

		var response bidiResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("could not decode answer to %s: %w: %w", method, ErrBiDiProtocol, err)
		}

		if response.ID == nil || *response.ID != id {
			continue // an event, or a stale answer: not what was asked for
		}

		if response.Type == "error" {
			return fmt.Errorf("%s: %s: %w", method, bidiMessage(response), ErrBiDiProtocol)
		}

		if out == nil {
			return nil
		}

		if err := json.Unmarshal(response.Result, out); err != nil {
			return fmt.Errorf("could not decode result of %s: %w: %w", method, ErrBiDiProtocol, err)
		}

		return nil
	}
}

// bidiMessage renders whichever part of an error answer carries the detail.
func bidiMessage(response bidiResponse) string {
	if response.Message != "" {
		return response.Message
	}

	return response.Error
}

// bidiCookie is one entry of the BiDi cookie jar. The value is wrapped, and
// the expiry is named differently than in the DevTools protocol.
type bidiCookie struct {
	Name   string  `json:"name"`
	Domain string  `json:"domain"`
	Expiry float64 `json:"expiry"`
	Value  struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"value"`
}

// cookies returns every cookie the browser holds.
//
// The jar is read whole and filtered by the caller on purpose: the BiDi domain
// filter matches exactly, so asking for "horoskopy.cz" would miss the cookie,
// which is stored under ".horoskopy.cz".
func (c *bidiConn) cookies() ([]cookie, error) {
	var result struct {
		Cookies []bidiCookie `json:"cookies"`
	}

	if err := c.call("storage.getCookies", nil, &result); err != nil {
		return nil, err
	}

	jar := make([]cookie, 0, len(result.Cookies))
	for _, found := range result.Cookies {
		if found.Value.Type != "string" {
			continue // a binary cookie; no session of ours looks like that
		}

		jar = append(jar, cookie{
			Name:    found.Name,
			Value:   found.Value.Value,
			Domain:  found.Domain,
			Expires: found.Expiry,
		})
	}

	return jar, nil
}

// openTab opens url in a new tab.
//
// BiDi splits this in two: a tab is created empty, and navigating it is a
// separate command. browsingContext.create quietly ignores a url parameter
// rather than refusing it, which opens a blank tab and waits forever.
func (c *bidiConn) openTab(url string) error {
	var created struct {
		Context string `json:"context"`
	}

	if err := c.call("browsingContext.create", map[string]any{"type": "tab"}, &created); err != nil {
		return err
	}

	return c.call("browsingContext.navigate", map[string]any{
		"context": created.Context,
		"url":     url,
		"wait":    "none",
	}, nil)
}

// closeBrowser asks Firefox to shut itself down.
func (c *bidiConn) closeBrowser() error {
	return c.call("browser.close", nil, nil)
}

// close releases the BiDi connection.
func (c *bidiConn) close() error {
	return c.ws.close()
}
