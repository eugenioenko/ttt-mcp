package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/eugenioenko/ttt-mcp/internal/rpc"
)

type Session struct {
	ID          string    `json:"session_id"`
	Name        string    `json:"name"`
	PID         int       `json:"pid"`
	Workspaces  []string  `json:"workspaces"`
	ActiveFile  string    `json:"active_file"`
	Version     string    `json:"version"`
	ConnectedAt time.Time `json:"connected_at"`
}

type Event struct {
	Sequence  uint64          `json:"sequence"`
	SessionID string          `json:"session_id"`
	Name      string          `json:"name"`
	Data      json.RawMessage `json:"data,omitempty"`
	Time      time.Time       `json:"time"`
}

type editor struct {
	info Session
	conn *rpc.Conn
	slot chan struct{}
}

type operationResult struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

type Hub struct {
	token      string
	mu         sync.Mutex
	editors    map[string]*editor
	operations map[string]chan operationResult
	events     []Event
	sequence   uint64
	stop       context.CancelFunc
}

func NewHub(token string) *Hub {
	return &Hub{token: token, editors: make(map[string]*editor), operations: make(map[string]chan operationResult)}
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (h *Hub) Serve(ctx context.Context, l net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	h.stop = cancel
	var peers sync.WaitGroup
	go func() { <-ctx.Done(); l.Close() }()
	defer peers.Wait()
	for {
		n, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		peers.Add(1)
		go func() { defer peers.Done(); h.accept(ctx, n) }()
	}
}

func (h *Hub) accept(ctx context.Context, n net.Conn) {
	var mu sync.Mutex
	role, sessionID := "", ""
	var conn *rpc.Conn
	ready := make(chan struct{})
	_ = n.SetReadDeadline(time.Now().Add(5 * time.Second))
	conn = rpc.New(n, func(callCtx context.Context, method string, params json.RawMessage) (any, error) {
		<-ready
		mu.Lock()
		defer mu.Unlock()
		if role == "" {
			if method != "hello" {
				return nil, errors.New("authenticate first")
			}
			var p struct {
				Token   string  `json:"token"`
				Role    string  `json:"role"`
				Session Session `json:"session"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			if subtle.ConstantTimeCompare([]byte(p.Token), []byte(h.token)) != 1 {
				return nil, errors.New("invalid service token")
			}
			if p.Role != "editor" && p.Role != "bridge" {
				return nil, errors.New("invalid connection role")
			}
			role = p.Role
			_ = n.SetReadDeadline(time.Time{})
			if role == "editor" {
				sessionID = randomID()
				p.Session.ID = sessionID
				p.Session.ConnectedAt = time.Now().UTC()
				if p.Session.Workspaces == nil {
					p.Session.Workspaces = []string{}
				}
				h.mu.Lock()
				h.editors[sessionID] = &editor{info: p.Session, conn: conn, slot: make(chan struct{}, 1)}
				h.eventLocked(sessionID, "session.connected", nil)
				h.mu.Unlock()
			}
			return map[string]any{"session_id": sessionID}, nil
		}
		currentRole, currentID := role, sessionID
		// Calls may wait for a completion notification from this same connection.
		mu.Unlock()
		result, err := h.handle(callCtx, conn, currentRole, currentID, method, params)
		mu.Lock()
		return result, err
	})
	close(ready)
	select {
	case <-ctx.Done():
		conn.Close()
		<-conn.Done()
	case <-conn.Done():
	}
	mu.Lock()
	id := sessionID
	mu.Unlock()
	if id != "" {
		h.mu.Lock()
		delete(h.editors, id)
		h.eventLocked(id, "session.disconnected", nil)
		h.mu.Unlock()
	}
}

func (h *Hub) eventLocked(id, name string, data json.RawMessage) {
	h.sequence++
	h.events = append(h.events, Event{Sequence: h.sequence, SessionID: id, Name: name, Data: data, Time: time.Now().UTC()})
	if len(h.events) > 256 {
		copy(h.events, h.events[len(h.events)-256:])
		h.events = h.events[:256]
	}
}

func (h *Hub) handle(ctx context.Context, conn *rpc.Conn, role, id, method string, params json.RawMessage) (any, error) {
	if role == "editor" {
		h.mu.Lock()
		defer h.mu.Unlock()
		switch method {
		case "operation/complete":
			var p operationResult
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			if ch := h.operations[id+"/"+p.ID]; ch != nil {
				select {
				case ch <- p:
				default:
				}
			}
		case "session/update":
			var p Session
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			if e := h.editors[id]; e != nil {
				e.info.ActiveFile = p.ActiveFile
				if p.Workspaces != nil {
					e.info.Workspaces = p.Workspaces
				}
			}
		case "editor/event":
			var p struct {
				Name string          `json:"name"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			h.eventLocked(id, p.Name, p.Data)
		default:
			return nil, fmt.Errorf("unknown editor method %q", method)
		}
		return map[string]bool{"ok": true}, nil
	}
	switch method {
	case "sessions/list":
		h.mu.Lock()
		defer h.mu.Unlock()
		sessions := make([]Session, 0, len(h.editors))
		for _, e := range h.editors {
			sessions = append(sessions, e.info)
		}
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
		return sessions, nil
	case "events/list":
		var p struct {
			Since     uint64 `json:"since"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		events := make([]Event, 0)
		for _, e := range h.events {
			if e.Sequence > p.Since && (p.SessionID == "" || p.SessionID == e.SessionID) {
				events = append(events, e)
			}
		}
		truncated := len(h.events) > 0 && p.Since < h.events[0].Sequence-1
		return map[string]any{"events": events, "cursor": h.sequence, "truncated": truncated}, nil
	case "sessions/call":
		var p struct {
			SessionID string   `json:"session_id"`
			Actions   []Action `json:"actions"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return h.run(ctx, p.SessionID, p.Actions)
	case "service/stop":
		if h.stop != nil {
			time.AfterFunc(100*time.Millisecond, h.stop)
		}
		return map[string]bool{"ok": true}, nil
	default:
		return nil, &rpc.Error{Code: -32601, Message: "unknown method: " + method}
	}
}

type Action struct {
	Action string         `json:"action" jsonschema:"Action name: screenshot debug session buffer commands command exec open click rclick hover drag scroll key type paste copy clipboard panel wait wait-for quit or editor.* and terminal.*"`
	Args   string         `json:"args,omitempty" jsonschema:"Action arguments; text is literal, coordinates use x y, drag uses x1 y1 x2 y2, scroll uses x y signed-count"`
	Params map[string]any `json:"params,omitempty" jsonschema:"Named parameters for open and editor or terminal API calls"`
}

func (h *Hub) run(ctx context.Context, id string, actions []Action) (any, error) {
	if len(actions) == 0 || len(actions) > 256 {
		return nil, errors.New("provide between 1 and 256 actions")
	}
	h.mu.Lock()
	e := h.editors[id]
	if id == "" && len(h.editors) == 1 {
		for _, candidate := range h.editors {
			e = candidate
		}
	}
	count := len(h.editors)
	h.mu.Unlock()
	if e == nil {
		if id == "" {
			return nil, fmt.Errorf("%d editor sessions available; call list_sessions and specify session_id", count)
		}
		return nil, fmt.Errorf("editor session %q is not connected", id)
	}
	select {
	case e.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.conn.Done():
		return nil, rpc.ErrClosed
	}
	defer func() { <-e.slot }()
	results := make([]json.RawMessage, 0, len(actions))
	for i, action := range actions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		opID := randomID()
		key := e.info.ID + "/" + opID
		done := make(chan operationResult, 1)
		h.mu.Lock()
		h.operations[key] = done
		h.mu.Unlock()
		// Keep this editor serialized until the current operation settles, even
		// when the requesting agent cancels its tool call.
		opCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		err := e.conn.Call(opCtx, "operation/run", map[string]any{"id": opID, "action": action}, nil)
		var completion operationResult
		if err == nil {
			select {
			case completion = <-done:
				if completion.Error != "" {
					err = errors.New(completion.Error)
				}
			case <-e.conn.Done():
				err = rpc.ErrClosed
			case <-opCtx.Done():
				err = opCtx.Err()
			case <-ctx.Done():
				cancelCtx, stop := context.WithTimeout(context.Background(), time.Second)
				_ = e.conn.Notify(cancelCtx, "operation/cancel", map[string]string{"id": opID})
				stop()
				select {
				case <-done:
				case <-e.conn.Done():
				case <-opCtx.Done():
					_ = e.conn.Close()
				}
				err = ctx.Err()
			}
		}
		if opCtx.Err() != nil {
			_ = e.conn.Close()
		}
		cancel()
		h.mu.Lock()
		delete(h.operations, key)
		h.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("action %d (%s): %w", i+1, action.Action, err)
		}
		results = append(results, completion.Result)
	}
	return map[string]any{"session_id": e.info.ID, "results": results, "completed": len(results)}, nil
}
