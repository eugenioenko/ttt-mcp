package bridge

import (
	"fmt"
	"strings"

	"github.com/eugenioenko/ttt-mcp/internal/service"
)

func ParseScript(script, separator string) ([]service.Action, error) {
	if separator == "" {
		separator = ";"
	}
	actions := make([]service.Action, 0)
	for _, raw := range strings.Split(script, separator) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name, args, _ := strings.Cut(raw, " ")
		args = strings.TrimSpace(args)
		switch name {
		case "click", "rclick", "hover", "drag", "scroll", "key", "type", "paste", "exec", "command", "screenshot", "debug", "wait", "wait-for", "copy", "clipboard", "panel", "quit", "shutdown", "commands", "buffer", "session":
		default:
			return nil, fmt.Errorf("unknown script action %q", name)
		}
		if name != "wait-for" && len(args) >= 2 && ((args[0] == '"' && args[len(args)-1] == '"') || (args[0] == '\'' && args[len(args)-1] == '\'')) {
			args = args[1 : len(args)-1]
		}
		if name == "copy" || name == "quit" || name == "shutdown" {
			if args != "" {
				return nil, fmt.Errorf("%s does not accept arguments", name)
			}
		}
		actions = append(actions, service.Action{Action: name, Args: args})
		if name == "quit" || name == "shutdown" {
			break
		}
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("script is empty")
	}
	return actions, nil
}
