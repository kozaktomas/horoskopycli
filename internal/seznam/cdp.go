package seznam

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The Chrome DevTools Protocol is used for exactly one thing here: reading the
// cookie jar of a browser this CLI started itself. Storage.getCookies works on
// the browser target, so no page attachment or session juggling is needed.

// cdpDialTimeout bounds connecting to the browser's debugging socket.
const cdpDialTimeout = 10 * time.Second

// ErrBrowserProtocol is returned when the browser answers a DevTools command
// with an error or with something unparseable.
var ErrBrowserProtocol = errors.New("browser protocol error")

// cookie is one entry of the browser cookie jar.
type cookie struct {
	Name    string  `json:"name"`
	Value   string  `json:"value"`
	Domain  string  `json:"domain"`
	Expires float64 `json:"expires"`
}

// expiresAt returns the cookie expiry as a time, or the zero time for session
// cookies, which CDP reports as -1.
func (c cookie) expiresAt() time.Time {
	if c.Expires <= 0 {
		return time.Time{}
	}

	return time.Unix(int64(c.Expires), 0)
}

// cdpConn is a DevTools protocol connection to a browser target.
type cdpConn struct {
	ws     *wsConn
	lastID int
}

// dialCDP connects to a browser's DevTools WebSocket endpoint.
func dialCDP(endpoint string) (*cdpConn, error) {
	ws, err := wsDial(endpoint, cdpDialTimeout)
	if err != nil {
		return nil, err
	}

	return &cdpConn{ws: ws}, nil
}

// cdpResponse is the envelope every DevTools command answer arrives in.
type cdpResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// call sends a DevTools command and decodes its result into out, which may be
// nil when the answer is not needed. Events arriving in the meantime are
// skipped, since this connection only ever has one command in flight.
func (c *cdpConn) call(method string, params map[string]any, out any) error {
	c.lastID++
	id := c.lastID

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
func (c *cdpConn) readResponse(id int, method string, out any) error {
	for {
		raw, err := c.ws.readText()
		if err != nil {
			return fmt.Errorf("could not read answer to %s: %w", method, err)
		}

		var response cdpResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("could not decode answer to %s: %w: %w", method, ErrBrowserProtocol, err)
		}

		if response.ID != id {
			continue // an event, or a stale answer: not what was asked for
		}

		if response.Error != nil {
			return fmt.Errorf("%s: %s: %w", method, response.Error.Message, ErrBrowserProtocol)
		}

		if out == nil {
			return nil
		}

		if err := json.Unmarshal(response.Result, out); err != nil {
			return fmt.Errorf("could not decode result of %s: %w: %w", method, ErrBrowserProtocol, err)
		}

		return nil
	}
}

// cookies returns every cookie in the browser's default context.
func (c *cdpConn) cookies() ([]cookie, error) {
	var result struct {
		Cookies []cookie `json:"cookies"`
	}

	if err := c.call("Storage.getCookies", nil, &result); err != nil {
		return nil, err
	}

	return result.Cookies, nil
}

// openTab opens url in a new browser tab. Visiting horoskopy.cz is what makes
// Seznam hand the login session over to that domain, so this is how the CLI
// nudges a stalled login over the finish line.
func (c *cdpConn) openTab(url string) error {
	return c.call("Target.createTarget", map[string]any{"url": url}, nil)
}

// closeBrowser asks the browser to shut itself down. Killing the process
// instead leaves its helper processes running, and those write the profile
// directory back out after it has been deleted.
func (c *cdpConn) closeBrowser() error {
	return c.call("Browser.close", nil, nil)
}

// close releases the DevTools connection.
func (c *cdpConn) close() error {
	return c.ws.close()
}
