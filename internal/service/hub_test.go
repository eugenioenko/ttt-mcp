package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenioenko/ttt-mcp/internal/rpc"
)

func testHub(t *testing.T) (*Hub, Descriptor) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub("secret")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("hub did not stop")
		}
	})
	return h, Descriptor{Endpoint: l.Addr().String(), Token: "secret"}
}

func testBridge(t *testing.T, d Descriptor) *rpc.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := Connect(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func testEditor(t *testing.T, d Descriptor, name string, fn func(Action) (any, error), onCancel ...func()) (*rpc.Conn, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n, err := dial(ctx, d.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	var c *rpc.Conn
	ready := make(chan struct{})
	c = rpc.New(n, func(ctx context.Context, method string, p json.RawMessage) (any, error) {
		<-ready
		if method == "operation/cancel" {
			if len(onCancel) > 0 {
				onCancel[0]()
			}
			return nil, nil
		}
		var req struct {
			ID     string `json:"id"`
			Action Action `json:"action"`
		}
		if err := json.Unmarshal(p, &req); err != nil {
			return nil, err
		}
		go func() {
			value, err := fn(req.Action)
			msg := map[string]any{"id": req.ID, "result": value}
			if err != nil {
				msg["error"] = err.Error()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = c.Notify(ctx, "operation/complete", msg)
		}()
		return map[string]bool{"queued": true}, nil
	})
	close(ready)
	t.Cleanup(func() { c.Close() })
	var result struct {
		ID string `json:"session_id"`
	}
	if err := c.Call(ctx, "hello", map[string]any{"role": "editor", "token": d.Token, "session": Session{Name: name, PID: 123, Workspaces: []string{"/" + name}}}, &result); err != nil {
		t.Fatal(err)
	}
	return c, result.ID
}

func TestCancelledBatchSettlesBeforeNextCall(t *testing.T) {
	_, d := testHub(t)
	started, cancelled := make(chan struct{}), make(chan struct{})
	var subsequent atomic.Int32
	_, id := testEditor(t, d, "alpha", func(a Action) (any, error) {
		if a.Action == "wait" {
			close(started)
			<-cancelled
			return nil, context.Canceled
		}
		subsequent.Add(1)
		return true, nil
	}, func() { close(cancelled) })
	a, b := testBridge(t, d), testBridge(t, d)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- a.Call(ctx, "sessions/call", map[string]any{"session_id": id, "actions": []Action{{Action: "wait"}, {Action: "type", Args: "must not execute"}}}, nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("operation never started")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	probeCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := b.Call(probeCtx, "sessions/call", map[string]any{"session_id": id, "actions": []Action{{Action: "buffer"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := subsequent.Load(); got != 1 {
		t.Fatalf("cancelled batch ran subsequent action: %d", got)
	}
}

func TestSessionDiscoveryRoutingAndDisconnect(t *testing.T) {
	_, d := testHub(t)
	a, idA := testEditor(t, d, "alpha", func(a Action) (any, error) { return "alpha:" + a.Args, nil })
	_, idB := testEditor(t, d, "beta", func(a Action) (any, error) { return "beta:" + a.Args, nil })
	b := testBridge(t, d)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var sessions []Session
	if err := b.Call(ctx, "sessions/list", nil, &sessions); err != nil || len(sessions) != 2 {
		t.Fatalf("sessions=%v err=%v", sessions, err)
	}
	p := map[string]any{"actions": []Action{{Action: "type", Args: "hello"}}}
	if err := b.Call(ctx, "sessions/call", p, nil); err == nil || !strings.Contains(err.Error(), "specify session_id") {
		t.Fatalf("ambiguous call = %v", err)
	}
	p["session_id"] = idB
	var result struct {
		SessionID string   `json:"session_id"`
		Results   []string `json:"results"`
	}
	if err := b.Call(ctx, "sessions/call", p, &result); err != nil || result.SessionID != idB || result.Results[0] != "beta:hello" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	a.Close()
	for {
		if err := b.Call(ctx, "sessions/list", nil, &sessions); err != nil {
			t.Fatal(err)
		}
		if len(sessions) == 1 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("closed editor remained registered")
		}
	}
	p["session_id"] = idA
	if err := b.Call(ctx, "sessions/call", p, nil); err == nil {
		t.Fatal("stale session accepted")
	}
	delete(p, "session_id")
	if err := b.Call(ctx, "sessions/call", p, &result); err != nil || result.SessionID != idB {
		t.Fatalf("single session auto select: %v", err)
	}
}

func TestActionsAreSerializedAcrossBridges(t *testing.T) {
	_, d := testHub(t)
	var active atomic.Int32
	var overlapped atomic.Bool
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	_, id := testEditor(t, d, "alpha", func(Action) (any, error) {
		if active.Add(1) > 1 {
			overlapped.Store(true)
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return true, nil
	})
	a, b := testBridge(t, d), testBridge(t, d)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errs := make(chan error, 2)
	p := map[string]any{"session_id": id, "actions": []Action{{Action: "type"}}}
	go func() { errs <- a.Call(ctx, "sessions/call", p, nil) }()
	<-started
	go func() { errs <- b.Call(ctx, "sessions/call", p, nil) }()
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if overlapped.Load() {
		t.Fatal("actions overlapped on the same editor")
	}
}

func TestAuthenticationAndEvents(t *testing.T) {
	h, d := testHub(t)
	bad := d
	bad.Token = "wrong"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c, err := Connect(ctx, bad); err == nil {
		c.Close()
		t.Fatal("invalid token accepted")
	}
	_, id := testEditor(t, d, "alpha", func(Action) (any, error) { return true, nil })
	h.mu.Lock()
	for range 300 {
		h.eventLocked(id, "editor.change", nil)
	}
	h.mu.Unlock()
	b := testBridge(t, d)
	var result struct {
		Events    []Event `json:"events"`
		Cursor    uint64  `json:"cursor"`
		Truncated bool    `json:"truncated"`
	}
	if err := b.Call(ctx, "events/list", map[string]any{"session_id": id}, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 256 || !result.Truncated {
		t.Fatalf("history=%+v", result)
	}
	if err := b.Call(ctx, "events/list", map[string]any{"since": result.Cursor}, &result); err != nil || len(result.Events) != 0 {
		t.Fatalf("cursor replay=%+v err=%v", result, err)
	}
}
