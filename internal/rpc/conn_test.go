package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBidirectionalCallsAndCancellation(t *testing.T) {
	a, b := net.Pipe()
	left := New(a, func(_ context.Context, _ string, p json.RawMessage) (any, error) {
		var v any
		_ = json.Unmarshal(p, &v)
		return v, nil
	})
	cancelled := make(chan struct{})
	right := New(b, func(ctx context.Context, method string, p json.RawMessage) (any, error) {
		if method == "wait" {
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		}
		var v any
		_ = json.Unmarshal(p, &v)
		return v, nil
	})
	t.Cleanup(func() { left.Close(); right.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := left
			if i%2 == 1 {
				c = right
			}
			var result int
			if err := c.Call(ctx, "echo", i, &result); err != nil || result != i {
				t.Errorf("call %d = %d, %v", i, result, err)
			}
		}()
	}
	wg.Wait()
	callCtx, stop := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() { errCh <- left.Call(callCtx, "wait", nil, nil) }()
	stop()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	// A cancelled-before-send call need not reach the peer; verify in-flight cancellation separately.
}

func TestInFlightCancellationReachesPeer(t *testing.T) {
	a, b := net.Pipe()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	left := New(a, nil)
	right := New(b, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	t.Cleanup(func() { left.Close(); right.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- left.Call(ctx, "wait", nil, nil) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request never started")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("peer was not cancelled")
	}
}

func TestClosedPeerReleasesCalls(t *testing.T) {
	a, b := net.Pipe()
	c := New(a, nil)
	t.Cleanup(func() { c.Close() })
	go func() { r := bufio.NewReader(b); _, _ = readMessage(r); b.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Call(ctx, "wait", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("call = %v", err)
	}
}

func TestFramingRejectsBadLengths(t *testing.T) {
	for _, header := range []string{"Content-Length: -1\r\n\r\n", "Content-Length: 33554433\r\n\r\n", "Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}", "Other: x\r\n\r\n", "Content-Length: nope\r\n\r\n"} {
		if _, err := readMessage(bufio.NewReader(strings.NewReader(header))); err == nil {
			t.Errorf("accepted %q", header)
		}
	}
	body := `{"jsonrpc":"2.0","id":"unicode","result":"世界"}`
	m, err := readMessage(bufio.NewReader(strings.NewReader(fmt.Sprintf("content-length: %d\r\n\r\n%s", len(body), body))))
	if err != nil || string(m.ID) != `"unicode"` {
		t.Fatalf("message=%+v err=%v", m, err)
	}
}
