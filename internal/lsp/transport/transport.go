// Package transport implements the LSP Content-Length JSON-RPC stream.
package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

const maxFrame = 32 << 20

type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *uint64         `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}
type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
type Transport struct {
	r              *bufio.Reader
	w              io.Writer
	writeMu        sync.Mutex
	pendingMu      sync.Mutex
	pending        map[uint64]chan Message
	nextID         uint64
	closed         chan struct{}
	closeOnce      sync.Once
	onRequest      func(context.Context, Message) (any, *ResponseError)
	onNotification func(Message)
}

func New(r io.Reader, w io.Writer) *Transport {
	return &Transport{r: bufio.NewReader(r), w: w, pending: map[uint64]chan Message{}, closed: make(chan struct{})}
}
func (t *Transport) OnRequest(f func(context.Context, Message) (any, *ResponseError)) {
	t.onRequest = f
}
func (t *Transport) OnNotification(f func(Message)) { t.onNotification = f }
func (t *Transport) Run(ctx context.Context) error {
	defer t.failAll(io.EOF)
	for {
		m, err := read(t.r)
		if err != nil {
			return err
		}
		if m.Method != "" {
			if m.ID != nil {
				go t.reply(ctx, m)
			} else if t.onNotification != nil {
				t.onNotification(m)
			}
			continue
		}
		if m.ID == nil {
			continue
		}
		t.pendingMu.Lock()
		ch := t.pending[*m.ID]
		delete(t.pending, *m.ID)
		t.pendingMu.Unlock()
		if ch != nil {
			ch <- m
			close(ch)
		}
	}
}
func (t *Transport) reply(ctx context.Context, m Message) {
	var result any
	var e *ResponseError
	if t.onRequest != nil {
		result, e = t.onRequest(ctx, m)
	} else {
		e = &ResponseError{Code: -32601, Message: "method not found"}
	}
	b, _ := json.Marshal(result)
	t.write(Message{JSONRPC: "2.0", ID: m.ID, Result: b, Error: e})
}
func (t *Transport) Request(ctx context.Context, method string, params any, result any) error {
	t.pendingMu.Lock()
	t.nextID++
	id := t.nextID
	ch := make(chan Message, 1)
	t.pending[id] = ch
	t.pendingMu.Unlock()
	if err := t.write(Message{JSONRPC: "2.0", ID: &id, Method: method, Params: mustJSON(params)}); err != nil {
		t.remove(id)
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return io.EOF
		}
		if m.Error != nil {
			return fmt.Errorf("lsp error %d: %s", m.Error.Code, m.Error.Message)
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-ctx.Done():
		t.remove(id)
		_ = t.Notify("$/cancelRequest", map[string]uint64{"id": id})
		return ctx.Err()
	case <-t.closed:
		return io.EOF
	}
}
func (t *Transport) Notify(method string, params any) error {
	return t.write(Message{JSONRPC: "2.0", Method: method, Params: mustJSON(params)})
}
func (t *Transport) remove(id uint64) {
	t.pendingMu.Lock()
	delete(t.pending, id)
	t.pendingMu.Unlock()
}
func (t *Transport) failAll(err error) {
	t.closeOnce.Do(func() {
		close(t.closed)
		t.pendingMu.Lock()
		defer t.pendingMu.Unlock()
		for id, ch := range t.pending {
			delete(t.pending, id)
			close(ch)
		}
	})
}
func (t *Transport) write(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > maxFrame {
		return errors.New("frame too large")
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = fmt.Fprintf(t.w, "Content-Length: %d\r\n\r\n", len(b))
	if err != nil {
		return err
	}
	_, err = t.w.Write(b)
	return err
}
func read(r *bufio.Reader) (Message, error) {
	var n = -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return Message{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return Message{}, errors.New("malformed lsp header")
		}
		if strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			v, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil || v < 0 || v > maxFrame {
				return Message{}, errors.New("invalid content length")
			}
			n = v
		}
	}
	if n < 0 {
		return Message{}, errors.New("missing content length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return Message{}, err
	}
	return m, nil
}
func mustJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}
