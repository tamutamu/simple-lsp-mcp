package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"testing"
)

func TestReadServerRequestWithStringID(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"config-17","method":"workspace/configuration","params":{"items":[]}}`)
	frame := append([]byte("Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"), body...)
	message, err := read(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(message.ID); got != `"config-17"` {
		t.Fatalf("request ID = %s, want quoted JSON-RPC string ID", got)
	}
	if message.Method != "workspace/configuration" {
		t.Fatalf("method = %q", message.Method)
	}
}

func TestReplyPreservesStringIDAndExclusiveResultOrError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      bool
		wantError bool
	}{
		{name: "success"},
		{name: "error", fail: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			transport := New(bytes.NewReader(nil), &output)
			transport.OnRequest(func(_ context.Context, m Message) (any, *ResponseError) {
				if got := string(m.ID); got != `"config-17"` {
					t.Fatalf("request ID = %s", got)
				}
				if tc.fail {
					return nil, &ResponseError{Code: -32602, Message: "bad params"}
				}
				return []any{map[string]any{"analysis": "strict"}}, nil
			})
			transport.reply(context.Background(), Message{
				JSONRPC: "2.0", ID: json.RawMessage(`"config-17"`),
				Method: "workspace/configuration",
			})
			response, err := read(bufio.NewReader(bytes.NewReader(output.Bytes())))
			if err != nil {
				t.Fatal(err)
			}
			if string(response.ID) != `"config-17"` {
				t.Fatalf("response ID = %s", response.ID)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(outputBody(t, output.Bytes()), &fields); err != nil {
				t.Fatal(err)
			}
			_, hasResult := fields["result"]
			_, hasError := fields["error"]
			if tc.wantError {
				if !hasError || hasResult || response.Error == nil || response.Error.Code != -32602 {
					t.Fatalf("error reply contains result or lacks error: %s", outputBody(t, output.Bytes()))
				}
			} else if !hasResult || hasError || response.Error != nil {
				t.Fatalf("success reply contains error or lacks result: %s", outputBody(t, output.Bytes()))
			}
		})
	}
}

func outputBody(t *testing.T, frame []byte) []byte {
	t.Helper()
	separator := []byte("\r\n\r\n")
	index := bytes.Index(frame, separator)
	if index < 0 {
		t.Fatalf("missing frame separator: %q", frame)
	}
	return frame[index+len(separator):]
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }

func TestNotifyRejectsShortFrameWrite(t *testing.T) {
	transport := New(bytes.NewReader(nil), shortWriter{})
	if err := transport.Notify("initialized", map[string]any{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Notify short write = %v, want io.ErrShortWrite", err)
	}
}
