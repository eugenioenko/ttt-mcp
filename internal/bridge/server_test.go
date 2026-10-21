package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/eugenioenko/ttt-mcp/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeBackend struct {
	ID      string
	Actions []service.Action
}

func (f *fakeBackend) Call(_ context.Context, method string, _ any, result any) error {
	data := `[]`
	if method == "sessions/list" {
		data = `[{"session_id":"alpha","name":"project"}]`
	}
	return json.Unmarshal([]byte(data), result)
}
func (f *fakeBackend) Run(_ context.Context, id string, actions []service.Action) (any, error) {
	f.ID = id
	f.Actions = actions
	return map[string]any{"completed": len(actions)}, nil
}

func TestToolsThroughMCP(t *testing.T) {
	backend := &fakeBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := NewServer(backend, "test")
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 15 {
		t.Fatalf("only %d tools", len(tools.Tools))
	}
	for _, tc := range []struct {
		name   string
		args   map[string]any
		action string
	}{
		{"execute_command", map[string]any{"session_id": "alpha", "command": "file.save"}, "command"},
		{"paste_text", map[string]any{"session_id": "alpha", "text": "hello; 世界\n"}, "paste"},
		{"mouse", map[string]any{"session_id": "alpha", "action": "rclick", "x": 4, "y": 5}, "rclick"},
		{"run_script", map[string]any{"session_id": "alpha", "script": "type hi; key ctrl+s; screenshot"}, "type"},
		{"call_api", map[string]any{"session_id": "alpha", "module": "ttt.editor", "function": "set_cursor", "args": []any{1, 2}}, "api.call"},
	} {
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v, %v", tc.name, result, err)
		}
		if backend.ID != "alpha" || backend.Actions[0].Action != tc.action {
			t.Fatalf("%s routed as %s %+v", tc.name, backend.ID, backend.Actions)
		}
	}
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "list_sessions", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("sessions: %+v %v", result, err)
	}
	if text := result.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "project") {
		t.Fatal(text)
	}
}

func TestScriptSyntax(t *testing.T) {
	actions, err := ParseScript("type 'hello; world'|key ctrl+s|screenshot", "|")
	if err != nil || len(actions) != 3 || actions[0].Args != "hello; world" {
		t.Fatalf("%+v %v", actions, err)
	}
	if _, err := ParseScript("bogus", ""); err == nil {
		t.Fatal("unknown action accepted")
	}
	if _, err := ParseScript("copy extra", ""); err == nil {
		t.Fatal("invalid copy accepted")
	}
	actions, err = ParseScript("quit; type unreachable", "")
	if err != nil || len(actions) != 1 {
		t.Fatalf("quit: %+v %v", actions, err)
	}
}
