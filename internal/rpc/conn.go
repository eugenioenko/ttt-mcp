package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxMessage = 32 << 20

var ErrClosed = errors.New("RPC connection closed")

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Handler func(context.Context, string, json.RawMessage) (any, error)

type Conn struct {
	net      net.Conn
	handler  Handler
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	write    chan struct{}
	next     atomic.Uint64
	mu       sync.Mutex
	pending  map[string]chan message
	incoming map[string]context.CancelFunc
}

func New(conn net.Conn, handler Handler) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{net: conn, handler: handler, ctx: ctx, cancel: cancel, done: make(chan struct{}), write: make(chan struct{}, 1), pending: make(map[string]chan message), incoming: make(map[string]context.CancelFunc)}
	c.write <- struct{}{}
	go c.read()
	return c
}

func (c *Conn) Done() <-chan struct{} { return c.done }
func (c *Conn) Close() error          { c.cancel(); return c.net.Close() }

func (c *Conn) Call(ctx context.Context, method string, params any, result any) error {
	id := strconv.FormatUint(c.next.Add(1), 10)
	ch := make(chan message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := c.send(ctx, message{ID: json.RawMessage(id), Method: method, Params: p}); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if result != nil {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-ctx.Done():
		go func() {
			notifyCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = c.Notify(notifyCtx, "$/cancelRequest", map[string]any{"id": json.RawMessage(id)})
		}()
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
}

func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.send(ctx, message{Method: method, Params: p})
}

func (c *Conn) send(ctx context.Context, m message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.write:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
	defer func() { c.write <- struct{}{} }()
	m.JSONRPC = "2.0"
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(data) > maxMessage {
		return errors.New("RPC message exceeds 32 MiB")
	}
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.net.SetWriteDeadline(deadline); err != nil {
		return err
	}
	defer c.net.SetWriteDeadline(time.Time{})
	frame := fmt.Appendf(nil, "Content-Length: %d\r\n\r\n", len(data))
	frame = append(frame, data...)
	for len(frame) > 0 {
		n, err := c.net.Write(frame)
		if err != nil {
			_ = c.Close()
			return err
		}
		if n == 0 {
			_ = c.Close()
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func readMessage(r *bufio.Reader) (message, error) {
	length := -1
	headerBytes := 0
	for {
		lineBytes, err := r.ReadSlice('\n')
		if err != nil {
			return message{}, err
		}
		line := string(lineBytes)
		headerBytes += len(line)
		if headerBytes > 8192 {
			return message{}, errors.New("RPC headers exceed 8 KiB")
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(key, "Content-Length") {
			if length != -1 {
				return message{}, errors.New("duplicate Content-Length")
			}
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil || length < 0 || length > maxMessage {
				return message{}, errors.New("invalid Content-Length")
			}
		}
	}
	if length < 0 {
		return message{}, errors.New("missing Content-Length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return message{}, err
	}
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.JSONRPC != "2.0" {
		return m, errors.New("expected JSON-RPC 2.0")
	}
	return m, nil
}

func (c *Conn) read() {
	defer close(c.done)
	defer c.cancel()
	defer c.net.Close()
	r := bufio.NewReader(c.net)
	for {
		m, err := readMessage(r)
		if err != nil {
			return
		}
		if m.Method == "" {
			c.mu.Lock()
			ch := c.pending[string(m.ID)]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
			continue
		}
		if m.Method == "$/cancelRequest" {
			var p struct {
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				c.mu.Lock()
				cancel := c.incoming[string(p.ID)]
				c.mu.Unlock()
				if cancel != nil {
					cancel()
				}
			}
			continue
		}
		// Notifications run in order so session updates precede subsequent calls.
		if len(m.ID) == 0 || string(m.ID) == "null" {
			if c.handler != nil {
				_, _ = c.handler(c.ctx, m.Method, m.Params)
			}
			continue
		}
		ctx, cancel := context.WithCancel(c.ctx)
		c.mu.Lock()
		if len(c.incoming) >= 128 {
			c.mu.Unlock()
			cancel()
			return
		}
		c.incoming[string(m.ID)] = cancel
		c.mu.Unlock()
		go func(m message) {
			defer cancel()
			defer func() { c.mu.Lock(); delete(c.incoming, string(m.ID)); c.mu.Unlock() }()
			var result any
			var err error
			if c.handler == nil {
				err = &Error{Code: -32601, Message: "unknown method: " + m.Method}
			} else {
				result, err = c.handler(ctx, m.Method, m.Params)
			}
			reply := message{ID: m.ID}
			if err == nil {
				reply.Result, err = json.Marshal(result)
			}
			if err != nil {
				var re *Error
				if !errors.As(err, &re) {
					re = &Error{Code: -32603, Message: err.Error()}
				}
				reply.Error = re
				reply.Result = nil
			}
			_ = c.send(c.ctx, reply)
		}(m)
	}
}
