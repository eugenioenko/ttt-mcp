package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eugenioenko/ttt-mcp/plugin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRealEditorsOverMCP(t *testing.T) {
	tttBinary := os.Getenv("TTT_BINARY")
	if tttBinary == "" {
		t.Skip("set TTT_BINARY to a ttt build with RPC and automation APIs")
	}
	tttBinary, err := filepath.Abs(tttBinary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dir := t.TempDir()
	homeDir, err := os.MkdirTemp("", "tttmcp-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(homeDir) })
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binDir, "ttt-mcp")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binary, ".")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	env := make([]string, 0)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "HOME", "USERPROFILE", "TTT_MCP_DIR", "TTT_CONFIG_DIR", "TTT_MCP_NAME", "PATH":
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "HOME="+homeDir, "USERPROFILE="+homeDir, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() { stop := exec.Command(binary, "service", "stop"); stop.Env = env; _ = stop.Run() })
	startEditor := func(name string) (*exec.Cmd, string) {
		project := filepath.Join(dir, name)
		if err := os.MkdirAll(project, 0755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(project, "notes.txt")
		if err := os.WriteFile(file, nil, 0644); err != nil {
			t.Fatal(err)
		}
		config := filepath.Join(dir, "config-"+name)
		pluginDir := filepath.Join(config, "plugins", "ttt-mcp")
		if err := os.MkdirAll(pluginDir, 0755); err != nil {
			t.Fatal(err)
		}
		manifestBytes, _ := plugin.Files.ReadFile("plugin.ttt.json")
		var manifest map[string]any
		_ = json.Unmarshal(manifestBytes, &manifest)
		permissions := manifest["permissions"].(map[string]any)
		if err := os.WriteFile(filepath.Join(pluginDir, "plugin.ttt.json"), manifestBytes, 0644); err != nil {
			t.Fatal(err)
		}
		source, _ := plugin.Files.ReadFile("init.lua")
		if err := os.WriteFile(filepath.Join(pluginDir, "init.lua"), source, 0644); err != nil {
			t.Fatal(err)
		}
		registry, _ := json.Marshal([]any{map[string]any{"name": "ttt-mcp", "version": "0.1.0", "enabled": true, "permissions": permissions, "grantsVersion": 2}})
		if err := os.WriteFile(filepath.Join(config, "plugins.ttt.json"), registry, 0644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(tttBinary, project, file, "--size", "100x30", "--exec", "wait-for \"MCP ◉\"; wait 30000")
		cmd.Env = append(append([]string{}, env...), "TTT_CONFIG_DIR="+config, "TTT_MCP_NAME="+name)
		log, err := os.Create(filepath.Join(dir, name+".log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			log.Close()
			if data, _ := os.ReadFile(log.Name()); strings.Contains(string(data), "WARNING: DATA RACE") {
				t.Errorf("%s editor reported a data race", name)
			}
			if t.Failed() {
				data, _ := os.ReadFile(log.Name())
				t.Logf("%s editor: %s", name, data)
			}
		})
		return cmd, file
	}
	alpha, fileA := startEditor("alpha")
	_, fileB := startEditor("beta")
	command := exec.Command(binary)
	command.Env = env
	client, err := mcp.NewClient(&mcp.Implementation{Name: "real-editor-test", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		r, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if r.IsError {
			data, _ := json.Marshal(r.Content)
			t.Fatalf("%s: %s", name, data)
		}
		data, err := json.Marshal(r.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatalf("%s response: %s: %v", name, data, err)
		}
		return value
	}
	var sessions []any
	for {
		response := call("list_sessions", map[string]any{})
		sessions = response["sessions"].([]any)
		if len(sessions) == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("editors never registered")
		case <-time.After(25 * time.Millisecond):
		}
	}
	ids := map[string]string{}
	for _, v := range sessions {
		s := v.(map[string]any)
		ids[s["name"].(string)] = s["session_id"].(string)
	}
	if ids["alpha"] == "" || ids["beta"] == "" || ids["alpha"] == ids["beta"] {
		t.Fatalf("sessions=%v", sessions)
	}
	r, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "type_text", Arguments: map[string]any{"text": "wrong target"}})
	if err != nil || !r.IsError {
		t.Fatalf("ambiguous target accepted: %+v %v", r, err)
	}
	text := "héllo; 世界\nfrom MCP"
	response := call("run_actions", map[string]any{"session_id": ids["alpha"], "actions": []any{
		map[string]any{"action": "paste", "args": text},
		map[string]any{"action": "key", "args": "ctrl+s"},
		map[string]any{"action": "buffer"},
		map[string]any{"action": "screenshot"},
	}})
	results := response["results"].([]any)
	if got := results[2].(map[string]any)["text"]; got != text+"\n" {
		t.Fatalf("buffer=%v", got)
	}
	if got := results[3].(string); !strings.Contains(got, "from MCP") {
		t.Fatalf("screen=%s", got)
	}
	if data, err := os.ReadFile(fileA); err != nil || string(data) != text+"\n" {
		t.Fatalf("saved=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(fileB); err != nil || len(data) != 0 {
		t.Fatalf("other editor changed: %q err=%v", data, err)
	}
	commands := call("list_commands", map[string]any{"session_id": ids["alpha"]})
	if raw, _ := json.Marshal(commands); !strings.Contains(string(raw), "file.save") {
		t.Fatal("command discovery missing save")
	}
	call("execute_command", map[string]any{"session_id": ids["alpha"], "command": "editor.undo"})
	call("execute_command", map[string]any{"session_id": ids["alpha"], "command": "editor.undo"})
	response = call("read_buffer", map[string]any{"session_id": ids["alpha"]})
	if got := response["results"].([]any)[0].(map[string]any)["text"]; got != "" {
		t.Fatalf("undo left %v", got)
	}
	call("call_api", map[string]any{"session_id": ids["beta"], "module": "ttt.editor", "function": "insert", "args": []any{1, 1, "API edit"}})
	response = call("read_buffer", map[string]any{"session_id": ids["beta"]})
	if got := response["results"].([]any)[0].(map[string]any)["text"]; got != "API edit" {
		t.Fatalf("API insert=%v", got)
	}
	call("run_script", map[string]any{"session_id": ids["beta"], "script": "key ctrl+s; screenshot"})
	call("call_api", map[string]any{"session_id": ids["beta"], "module": "ttt", "function": "key", "args": []any{"ctrl+s"}})
	call("mouse", map[string]any{"session_id": ids["alpha"], "action": "hover", "x": 40, "y": 5})
	call("mouse", map[string]any{"session_id": ids["alpha"], "action": "rclick", "x": 40, "y": 5})
	call("press_key", map[string]any{"session_id": ids["alpha"], "key": "escape"})
	state := call("debug_state", map[string]any{"session_id": ids["alpha"]})
	if state["results"].([]any)[0].(map[string]any)["screen"] == nil {
		t.Fatal("debug state missing screen")
	}
	events := call("read_events", map[string]any{"session_id": ids["alpha"]})
	if len(events["events"].([]any)) == 0 {
		t.Fatal("no editor events observed")
	}
	oldIDs := ids
	stopService := exec.CommandContext(ctx, binary, "service", "stop")
	stopService.Env = env
	if out, err := stopService.CombinedOutput(); err != nil {
		t.Fatalf("stop service: %v: %s", err, out)
	}
	for {
		response = call("list_sessions", map[string]any{})
		current := map[string]string{}
		for _, v := range response["sessions"].([]any) {
			s := v.(map[string]any)
			current[s["name"].(string)] = s["session_id"].(string)
		}
		if len(current) == 2 && current["alpha"] != oldIDs["alpha"] && current["beta"] != oldIDs["beta"] {
			ids = current
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("editors did not reconnect after service restart")
		case <-time.After(25 * time.Millisecond):
		}
	}
	if err := alpha.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	for {
		response = call("list_sessions", map[string]any{})
		if len(response["sessions"].([]any)) == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("closed editor still registered")
		case <-time.After(10 * time.Millisecond):
		}
	}
	response = call("read_buffer", map[string]any{})
	if response["session_id"] != ids["beta"] {
		t.Fatal(fmt.Sprint(response))
	}
}
