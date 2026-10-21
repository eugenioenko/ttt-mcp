package bridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/eugenioenko/ttt-mcp/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Backend interface {
	Call(context.Context, string, any, any) error
	Run(context.Context, string, []service.Action) (any, error)
}

type Target struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Target editor session ID from list_sessions. Required when multiple editors are connected."`
}

func NewServer(backend Backend, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "ttt-mcp", Version: version}, &mcp.ServerOptions{
		Instructions: "Controls live ttt editor sessions through their Lua plugins. First call list_sessions and choose the workspace. Always pass session_id when several editors are open. Commands are discovered at runtime. Screen coordinates are zero-based terminal cells; ttt.editor API lines/columns are one-based rune indices; debug_state positions are zero-based. Actions complete after main-thread handling and redraw. Use wait_for for asynchronous results. run_actions serializes a sequence per editor. Call read_events with its returned cursor to observe editor changes.",
		Capabilities: &mcp.ServerCapabilities{},
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "list_sessions", Description: "Discover running ttt editors: session ID, name, PID, workspace paths, active file and version.", Annotations: readOnly}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		var sessions []service.Session
		err := backend.Call(ctx, "sessions/list", nil, &sessions)
		return nil, map[string]any{"sessions": sessions}, err
	})
	add := func(name, description, action string, readonly bool) {
		t := &mcp.Tool{Name: name, Description: description}
		if readonly {
			t.Annotations = readOnly
		}
		mcp.AddTool(s, t, func(ctx context.Context, _ *mcp.CallToolRequest, in Target) (*mcp.CallToolResult, any, error) {
			r, err := backend.Run(ctx, in.SessionID, []service.Action{{Action: action}})
			return nil, r, err
		})
	}
	add("list_commands", "List every registered editor and plugin command with its ID and title.", "commands", true)
	add("screenshot", "Capture the rendered terminal screen as plain text.", "screenshot", true)
	add("debug_state", "Inspect the editor state, tabs, selection, focus, panels, output, terminal tails and widget tree. Buffer text is capped at 1000 lines; read_buffer returns full text.", "debug", true)
	add("read_buffer", "Read the complete active buffer, path and cursor position.", "buffer", true)
	add("list_api", "Discover callable functions in the ttt, ttt.editor and ttt.terminal Lua modules.", "api.list", true)
	add("read_clipboard", "Read the editor clipboard.", "clipboard", true)
	type TextInput struct {
		Target
		Text string `json:"text"`
	}
	for _, spec := range []struct{ name, description, action string }{
		{"type_text", "Type literal text as key presses in the focused UI. Use paste_text for multiline or large text.", "type"},
		{"paste_text", "Paste literal text through the editor paste path as one undoable edit.", "paste"},
		{"wait_for", "Wait up to five seconds for literal text to appear on screen.", "wait-for"},
	} {
		mcp.AddTool(s, &mcp.Tool{Name: spec.name, Description: spec.description}, func(ctx context.Context, _ *mcp.CallToolRequest, in TextInput) (*mcp.CallToolResult, any, error) {
			text := in.Text
			if spec.action == "wait-for" {
				quoted, _ := json.Marshal(text)
				text = string(quoted)
			}
			r, err := backend.Run(ctx, in.SessionID, []service.Action{{Action: spec.action, Args: text}})
			return nil, r, err
		})
	}
	type CommandInput struct {
		Target
		Command string `json:"command" jsonschema:"Command ID from list_commands, or exact command title"`
		ByTitle bool   `json:"by_title,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "execute_command", Description: "Execute any registered command by ID or exact title."}, func(ctx context.Context, _ *mcp.CallToolRequest, in CommandInput) (*mcp.CallToolResult, any, error) {
		action := "command"
		if in.ByTitle {
			action = "exec"
		}
		r, err := backend.Run(ctx, in.SessionID, []service.Action{{Action: action, Args: in.Command}})
		return nil, r, err
	})
	type KeyInput struct {
		Target
		Key string `json:"key" jsonschema:"Key combo or chord, e.g. ctrl+s, enter, ctrl+k ctrl+s"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "press_key", Description: "Press a key, modifier combination, or multi-step chord."}, func(ctx context.Context, _ *mcp.CallToolRequest, in KeyInput) (*mcp.CallToolResult, any, error) {
		r, err := backend.Run(ctx, in.SessionID, []service.Action{{Action: "key", Args: in.Key}})
		return nil, r, err
	})
	type OpenInput struct {
		Target
		Path     string `json:"path"`
		Line     int    `json:"line,omitempty" jsonschema:"Optional one-based line"`
		Readonly bool   `json:"readonly,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "open_file", Description: "Open a file in an editor tab, optionally at a line or read-only."}, func(ctx context.Context, _ *mcp.CallToolRequest, in OpenInput) (*mcp.CallToolResult, any, error) {
		r, err := backend.Run(ctx, in.SessionID, []service.Action{{Action: "open", Params: map[string]any{"path": in.Path, "line": in.Line, "readonly": in.Readonly}}})
		return nil, r, err
	})
	type MouseInput struct {
		Target
		Action string `json:"action" jsonschema:"click, rclick, hover, drag, or scroll"`
		X      int    `json:"x"`
		Y      int    `json:"y"`
		ToX    int    `json:"to_x,omitempty"`
		ToY    int    `json:"to_y,omitempty"`
		Count  int    `json:"count,omitempty" jsonschema:"Scroll wheel ticks: positive down, negative up, maximum magnitude 1000"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "mouse", Description: "Click, right-click, hover, drag or scroll at zero-based terminal cell coordinates."}, func(ctx context.Context, _ *mcp.CallToolRequest, in MouseInput) (*mcp.CallToolResult, any, error) {
		args := fmt.Sprintf("%d %d", in.X, in.Y)
		switch in.Action {
		case "click", "rclick", "hover":
		case "drag":
			args += fmt.Sprintf(" %d %d", in.ToX, in.ToY)
		case "scroll":
			args += fmt.Sprintf(" %d", in.Count)
		default:
			return nil, nil, fmt.Errorf("unknown mouse action %q", in.Action)
		}
		r, err := backend.Run(ctx, in.SessionID, []service.Action{{Action: in.Action, Args: args}})
		return nil, r, err
	})
	type PanelInput struct {
		Target
		Panel string `json:"panel"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "show_panel", Description: "Show and focus a bottom panel by ID, such as output or terminal."}, func(ctx context.Context, _ *mcp.CallToolRequest, in PanelInput) (*mcp.CallToolResult, any, error) {
		r, err := backend.Run(ctx, in.SessionID, []service.Action{{Action: "panel", Args: in.Panel}})
		return nil, r, err
	})
	type ActionsInput struct {
		Target
		Actions []service.Action `json:"actions" jsonschema:"Ordered actions; each is acknowledged before the next begins. Up to 256 actions. Screenshot/debug return data directly; optional args writes through the plugin filesystem. Use api.call with params module/function/args for the Lua APIs."`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "run_actions", Description: "Run an ordered batch of editor actions without interleaving other agents' actions. Stops on the first failure."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ActionsInput) (*mcp.CallToolResult, any, error) {
		r, err := backend.Run(ctx, in.SessionID, in.Actions)
		return nil, r, err
	})
	type ScriptInput struct {
		Target
		Script    string `json:"script"`
		Separator string `json:"separator,omitempty" jsonschema:"Default is semicolon. Set a custom separator if text contains semicolons."`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "run_script", Description: "Run the --exec/--listen action syntax over plugin RPC: click, rclick, hover, drag, key, type, paste, exec, copy, panel, screenshot, debug, wait, wait-for, quit. Screenshots and debug return directly when no path is supplied. No --listen required."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ScriptInput) (*mcp.CallToolResult, any, error) {
		actions, err := ParseScript(in.Script, in.Separator)
		if err != nil {
			return nil, nil, err
		}
		r, err := backend.Run(ctx, in.SessionID, actions)
		return nil, r, err
	})
	type APIInput struct {
		Target
		Module   string `json:"module" jsonschema:"ttt, ttt.editor, or ttt.terminal"`
		Function string `json:"function" jsonschema:"Function from list_api. The function must accept JSON values; callbacks and Lua functions cannot be passed."`
		Args     []any  `json:"args,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "call_api", Description: "Call a public Lua API with JSON arguments. Supports editor reads, undoable edits, cursor/selection, scrolling and terminal operations. Discover functions with list_api; editor coordinates are one-based."}, func(ctx context.Context, _ *mcp.CallToolRequest, in APIInput) (*mcp.CallToolResult, any, error) {
		r, err := backend.Run(ctx, in.SessionID, []service.Action{{Action: "api.call", Params: map[string]any{"module": in.Module, "function": in.Function, "args": in.Args}}})
		return nil, r, err
	})
	type EventsInput struct {
		Target
		Since uint64 `json:"since,omitempty" jsonschema:"Cursor from a previous read_events response; zero reads retained history"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "read_events", Description: "Read retained session/file/editor events after a cursor. Keeps the latest 256 events; truncated reports a history gap. Omit session_id to observe all editors.", Annotations: readOnly}, func(ctx context.Context, _ *mcp.CallToolRequest, in EventsInput) (*mcp.CallToolResult, any, error) {
		var result json.RawMessage
		err := backend.Call(ctx, "events/list", map[string]any{"since": in.Since, "session_id": in.SessionID}, &result)
		if err != nil {
			return nil, nil, err
		}
		var value any
		err = json.Unmarshal(result, &value)
		return nil, value, err
	})
	return s
}
