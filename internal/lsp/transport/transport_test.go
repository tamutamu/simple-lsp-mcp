package transport

import (
	"bufio"
	"bytes"
	"fmt"
	"testing"
)

func TestReadFrames(t *testing.T) {
	body := "{\"jsonrpc\":\"2.0\",\"method\":\"ready\"}"
	input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	m, err := read(bufio.NewReader(bytes.NewBufferString(input)))
	if err != nil || m.Method != "ready" {
		t.Fatalf("read = %#v, %v", m, err)
	}
}
func TestReadRejectsMissingLength(t *testing.T) {
	if _, err := read(bufio.NewReader(bytes.NewBufferString("X: 1\r\n\r\n{}"))); err == nil {
		t.Fatal("expected error")
	}
}
