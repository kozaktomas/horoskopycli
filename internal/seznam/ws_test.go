package seznam

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// writeServerFrame writes one frame the way a server does: never masked.
func writeServerFrame(t *testing.T, conn net.Conn, opcode byte, final bool, payload []byte) {
	t.Helper()

	first := opcode
	if final {
		first |= wsFinalFrame
	}

	header := []byte{first, byte(len(payload))}
	if len(payload) > 125 {
		header = []byte{first, wsLength16, 0, 0}
		binary.BigEndian.PutUint16(header[2:], uint16(len(payload)))
	}

	if _, err := conn.Write(append(header, payload...)); err != nil {
		t.Errorf("could not write test frame: %v", err)
	}
}

// readMaskedFrame reads one masked client frame and unmasks its payload. It is
// safe to call from a goroutine, unlike its testing.T wrapper below.
func readMaskedFrame(conn net.Conn) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}

	if header[1]&wsMaskedFrame == 0 {
		return nil, errors.New("client frame is not masked")
	}

	length, err := readFrameLength(conn, header[1]&wsLengthMask)
	if err != nil {
		return nil, err
	}

	mask := make([]byte, 4)
	if _, err := io.ReadFull(conn, mask); err != nil {
		return nil, err
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}

	for i := range payload {
		payload[i] ^= mask[i%4]
	}

	return payload, nil
}

// readFrameLength resolves the extended length fields of a frame header.
func readFrameLength(conn net.Conn, short byte) (uint64, error) {
	switch short {
	case wsLength16:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(conn, extended); err != nil {
			return 0, err
		}

		return uint64(binary.BigEndian.Uint16(extended)), nil
	case wsLength64:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(conn, extended); err != nil {
			return 0, err
		}

		return binary.BigEndian.Uint64(extended), nil
	default:
		return uint64(short), nil
	}
}

// readClientFrame reads one masked frame from a client and returns its payload.
func readClientFrame(t *testing.T, conn net.Conn) []byte {
	t.Helper()

	payload, err := readMaskedFrame(conn)
	if err != nil {
		t.Fatalf("could not read client frame: %v", err)
	}

	return payload
}

func TestWSHeader(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		length int
		want   []byte
	}{
		"tiny frame":     {length: 5, want: []byte{0x81, 0x85}},
		"largest 7 bit":  {length: 125, want: []byte{0x81, 0xFD}},
		"16 bit length":  {length: 126, want: []byte{0x81, 0xFE, 0x00, 0x7E}},
		"largest 16 bit": {length: 0xFFFF, want: []byte{0x81, 0xFE, 0xFF, 0xFF}},
		"64 bit length":  {length: 0x10000, want: []byte{0x81, 0xFF, 0, 0, 0, 0, 0, 0x01, 0x00, 0x00}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := wsHeader(tt.length)
			if len(got) != len(tt.want) {
				t.Fatalf("wsHeader(%d) = % x, want % x", tt.length, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("wsHeader(%d) = % x, want % x", tt.length, got, tt.want)
				}
			}
		})
	}
}

func TestWSConnWriteTextIsMasked(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() {
		_ = client.Close()
	}()

	payload := []byte(`{"id":1,"method":"Storage.getCookies"}`)
	go func() {
		if err := newWSConn(client).writeText(payload); err != nil {
			t.Errorf("writeText returned %v", err)
		}
	}()

	if got := string(readClientFrame(t, server)); got != string(payload) {
		t.Errorf("server received %q, want %q", got, payload)
	}
}

func TestWSConnReadTextJoinsFramesAndSkipsControl(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() {
		_ = client.Close()
	}()

	go func() {
		writeServerFrame(t, server, wsOpcodePing, true, []byte("hello?"))
		writeServerFrame(t, server, wsOpcodeText, false, []byte(`{"id":`))
		writeServerFrame(t, server, wsOpcodeContinuation, false, []byte(`1,"result"`))
		writeServerFrame(t, server, wsOpcodeContinuation, true, []byte(`:{}}`))
	}()

	got, err := newWSConn(client).readText()
	if err != nil {
		t.Fatalf("readText returned %v", err)
	}
	if string(got) != `{"id":1,"result":{}}` {
		t.Errorf("readText = %q, want the joined message", got)
	}
}

func TestWSConnReadTextReportsClose(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() {
		_ = client.Close()
	}()

	go writeServerFrame(t, server, wsOpcodeClose, true, nil)

	if _, err := newWSConn(client).readText(); !errors.Is(err, io.EOF) {
		t.Errorf("readText after close = %v, want io.EOF", err)
	}
}

func TestWSConnReadTextRefusesHugeFrames(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() {
		_ = client.Close()
	}()

	go func() {
		header := make([]byte, 10)
		header[0] = wsFinalFrame | wsOpcodeText
		header[1] = wsLength64
		binary.BigEndian.PutUint64(header[2:], wsMaxMessage+1)
		if _, err := server.Write(header); err != nil {
			t.Errorf("could not write oversized header: %v", err)
		}
	}()

	if _, err := newWSConn(client).readText(); !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("readText of an oversized frame = %v, want ErrMessageTooLarge", err)
	}
}

// serveHandshake answers one connection with the given status line, then hands
// the socket to handle.
func serveHandshake(t *testing.T, status string, handle func(net.Conn)) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not listen: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	// Every connection is served, not just the first, so that a stray dial
	// from elsewhere cannot starve the client under test.
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func() {
				buf := make([]byte, 1024)
				if _, err := conn.Read(buf); err != nil {
					return
				}
				if _, err := conn.Write([]byte(status + "\r\nUpgrade: websocket\r\n\r\n")); err != nil {
					return
				}

				handle(conn)
			}()
		}
	}()

	return "ws://" + listener.Addr().String() + "/devtools/browser/abc"
}

func TestWSDialSpeaksToAnUpgradedServer(t *testing.T) {
	t.Parallel()

	endpoint := serveHandshake(t, "HTTP/1.1 101 Switching Protocols", func(conn net.Conn) {
		writeServerFrame(t, conn, wsOpcodeText, true, []byte("pong"))
	})

	conn, err := wsDial(endpoint, time.Second)
	if err != nil {
		t.Fatalf("wsDial returned %v", err)
	}
	defer func() {
		_ = conn.close()
	}()

	got, err := conn.readText()
	if err != nil {
		t.Fatalf("readText returned %v", err)
	}
	if string(got) != "pong" {
		t.Errorf("readText = %q, want %q", got, "pong")
	}
}

func TestWSDialKeepsBytesArrivingWithTheHandshake(t *testing.T) {
	t.Parallel()

	// A browser often sends its first frame in the same packet as the upgrade
	// response. Those bytes land in the handshake reader's buffer, and a
	// connection that started a fresh reader would wait for them forever.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not listen: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}

		buf := make([]byte, 1024)
		if _, err := conn.Read(buf); err != nil {
			return
		}

		response := []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\n")
		response = append(response, wsFinalFrame|wsOpcodeText, byte(len("pong")))
		response = append(response, []byte("pong")...)

		if _, err := conn.Write(response); err != nil {
			t.Errorf("could not write the response: %v", err)
		}
	}()

	conn, err := wsDial("ws://"+listener.Addr().String()+"/devtools/browser/abc", time.Second)
	if err != nil {
		t.Fatalf("wsDial returned %v", err)
	}
	defer func() {
		_ = conn.close()
	}()

	got, err := conn.readText()
	if err != nil {
		t.Fatalf("readText returned %v", err)
	}
	if string(got) != "pong" {
		t.Errorf("readText = %q, want %q", got, "pong")
	}
}

func TestWSDialRejectsARefusedUpgrade(t *testing.T) {
	t.Parallel()

	endpoint := serveHandshake(t, "HTTP/1.1 403 Forbidden", func(_ net.Conn) {})

	if _, err := wsDial(endpoint, time.Second); !errors.Is(err, ErrHandshake) {
		t.Errorf("wsDial against a refusing server = %v, want ErrHandshake", err)
	}
}

func TestWSDialFailsWhenNothingIsListening(t *testing.T) {
	t.Parallel()

	// Port 0 is never connectable, and unlike a port freed on purpose it can
	// never be handed to another test's listener in the meantime.
	if _, err := wsDial("ws://127.0.0.1:0/", time.Second); err == nil {
		t.Error("wsDial with nothing to connect to should fail")
	}
}
