package seznam

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// The DevTools protocol only speaks WebSocket, and the handful of frames this
// package needs — one text message out, one text message in — is far less code
// than a dependency would cost. RFC 6455 in the small: no compression, no
// fragmentation on send, no subprotocols.

// wsMaxMessage bounds a single incoming message. Cookie dumps are the largest
// thing the CLI reads and stay far below this.
const wsMaxMessage = 8 << 20

// wsCommandTimeout bounds a single exchange with the browser. Without it a
// browser that never answers would hang the CLI for good: the login loop only
// checks its own deadline between commands, never during one.
const wsCommandTimeout = 30 * time.Second

// Frame header bits and opcodes used below.
const (
	wsFinalFrame  = 0x80
	wsMaskedFrame = 0x80
	wsOpcodeMask  = 0x0F
	wsLengthMask  = 0x7F

	wsOpcodeContinuation = 0x0
	wsOpcodeText         = 0x1
	wsOpcodeClose        = 0x8
	wsOpcodePing         = 0x9
	wsOpcodePong         = 0xA

	wsLength16 = 126
	wsLength64 = 127
)

// ErrHandshake is returned when a server refuses the WebSocket upgrade.
var ErrHandshake = errors.New("websocket handshake failed")

// ErrMessageTooLarge is returned when a peer announces a frame larger than
// wsMaxMessage, rather than letting it allocate unbounded memory.
var ErrMessageTooLarge = errors.New("websocket message too large")

// wsConn is a minimal client-side WebSocket connection.
type wsConn struct {
	conn net.Conn
	buf  *bufio.Reader
}

// wsDial opens a WebSocket connection to a ws:// URL and performs the HTTP
// upgrade handshake. It returns ErrHandshake if the server answers with
// anything other than 101 Switching Protocols.
func wsDial(rawURL string, timeout time.Duration) (*wsConn, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("could not parse %q: %w", rawURL, err)
	}

	conn, err := net.DialTimeout("tcp", parsed.Host, timeout)
	if err != nil {
		return nil, fmt.Errorf("could not connect to %s: %w", parsed.Host, err)
	}

	reader, err := wsHandshake(conn, parsed)
	if err != nil {
		_ = conn.Close()

		return nil, err
	}

	// The handshake reader may already hold the first bytes the server sent
	// after its headers, so the connection keeps reading through it instead of
	// starting a fresh reader and losing them.
	return &wsConn{conn: conn, buf: reader}, nil
}

// newWSConn wraps an already connected and upgraded socket.
func newWSConn(conn net.Conn) *wsConn {
	return &wsConn{conn: conn, buf: bufio.NewReader(conn)}
}

// wsHandshake sends the upgrade request for target and consumes the response
// headers. It returns the buffered reader they were read through, positioned
// at the first frame.
func wsHandshake(conn net.Conn, target *url.URL) (*bufio.Reader, error) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("could not generate websocket key: %w", err)
	}

	request := strings.Join([]string{
		"GET " + target.RequestURI() + " HTTP/1.1",
		"Host: " + target.Host,
		"Upgrade: websocket",
		"Connection: Upgrade",
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key),
		"Sec-WebSocket-Version: 13",
		"", "",
	}, "\r\n")

	if _, err := conn.Write([]byte(request)); err != nil {
		return nil, fmt.Errorf("could not send websocket handshake: %w", err)
	}

	reader := bufio.NewReader(conn)

	status, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("could not read websocket handshake response: %w", err)
	}
	if !strings.Contains(status, "101") {
		return nil, fmt.Errorf("%w: %s", ErrHandshake, strings.TrimSpace(status))
	}

	if err := drainHeaders(reader); err != nil {
		return nil, err
	}

	return reader, nil
}

// drainHeaders reads response headers up to and including the blank line that
// ends them.
func drainHeaders(reader *bufio.Reader) error {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("could not read websocket handshake headers: %w", err)
		}

		if strings.TrimSpace(line) == "" {
			return nil
		}
	}
}

// writeText sends payload as a single masked text frame, as RFC 6455 requires
// of clients. The write is bounded by wsCommandTimeout.
func (w *wsConn) writeText(payload []byte) error {
	if err := w.conn.SetWriteDeadline(time.Now().Add(wsCommandTimeout)); err != nil {
		return fmt.Errorf("could not set a write deadline: %w", err)
	}

	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return fmt.Errorf("could not generate websocket mask: %w", err)
	}

	frame := append(wsHeader(len(payload)), mask...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}

	if _, err := w.conn.Write(frame); err != nil {
		return fmt.Errorf("could not write websocket frame: %w", err)
	}

	return nil
}

// wsHeader builds the two-, four- or ten-byte header of a masked final text
// frame carrying length bytes.
func wsHeader(length int) []byte {
	switch {
	case length <= 125:
		return []byte{wsFinalFrame | wsOpcodeText, wsMaskedFrame | byte(length)}
	case length <= 0xFFFF:
		header := []byte{wsFinalFrame | wsOpcodeText, wsMaskedFrame | wsLength16, 0, 0}
		binary.BigEndian.PutUint16(header[2:], uint16(length))

		return header
	default:
		header := make([]byte, 10)
		header[0] = wsFinalFrame | wsOpcodeText
		header[1] = wsMaskedFrame | wsLength64
		binary.BigEndian.PutUint64(header[2:], uint64(length))

		return header
	}
}

// readText returns the next complete text message, joining continuation frames
// and skipping ping and pong control frames. It returns io.EOF when the peer
// closes the connection, and gives up after wsCommandTimeout so that a browser
// which stops answering surfaces as an error rather than as a hang.
func (w *wsConn) readText() ([]byte, error) {
	if err := w.conn.SetReadDeadline(time.Now().Add(wsCommandTimeout)); err != nil {
		return nil, fmt.Errorf("could not set a read deadline: %w", err)
	}

	var message []byte

	for {
		final, opcode, payload, err := w.readFrame()
		if err != nil {
			return nil, err
		}

		switch opcode {
		case wsOpcodeClose:
			return nil, io.EOF
		case wsOpcodePing, wsOpcodePong:
			continue
		case wsOpcodeText, wsOpcodeContinuation:
			message = append(message, payload...)
		}

		if final {
			return message, nil
		}
	}
}

// readFrame reads one frame and reports whether it finishes a message. Frames
// from a server are never masked, so no unmasking is needed.
func (w *wsConn) readFrame() (final bool, opcode byte, payload []byte, err error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(w.buf, header); err != nil {
		return false, 0, nil, fmt.Errorf("could not read websocket frame header: %w", err)
	}

	length, err := w.readLength(header[1] & wsLengthMask)
	if err != nil {
		return false, 0, nil, err
	}

	payload = make([]byte, length)
	if _, err := io.ReadFull(w.buf, payload); err != nil {
		return false, 0, nil, fmt.Errorf("could not read websocket payload: %w", err)
	}

	return header[0]&wsFinalFrame != 0, header[0] & wsOpcodeMask, payload, nil
}

// readLength resolves the frame length, reading the 16- or 64-bit extension
// when the 7-bit field says so. It returns ErrMessageTooLarge for frames above
// wsMaxMessage.
func (w *wsConn) readLength(short byte) (uint64, error) {
	length := uint64(short)

	switch short {
	case wsLength16:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(w.buf, extended); err != nil {
			return 0, fmt.Errorf("could not read websocket frame length: %w", err)
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	case wsLength64:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(w.buf, extended); err != nil {
			return 0, fmt.Errorf("could not read websocket frame length: %w", err)
		}
		length = binary.BigEndian.Uint64(extended)
	}

	if length > wsMaxMessage {
		return 0, fmt.Errorf("%w: %d bytes", ErrMessageTooLarge, length)
	}

	return length, nil
}

// close releases the underlying socket.
func (w *wsConn) close() error {
	if err := w.conn.Close(); err != nil {
		return fmt.Errorf("could not close websocket: %w", err)
	}

	return nil
}
