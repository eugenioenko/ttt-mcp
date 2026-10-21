package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/eugenioenko/ttt-mcp/internal/bridge"
	"github.com/eugenioenko/ttt-mcp/internal/service"
	"github.com/eugenioenko/ttt-mcp/plugin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ttt-mcp:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-v":
			fmt.Println(version)
			return nil
		case "--help", "-h", "help":
			fmt.Print(usage)
			return nil
		case "install-plugin":
			return install(args[1:])
		case "service":
			return runService(ctx, args[1:])
		case "sessions":
			c, err := bridge.DefaultClient()
			if err != nil {
				return err
			}
			var sessions []service.Session
			if err := c.Call(ctx, "sessions/list", nil, &sessions); err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(sessions)
		case "stdio":
			args = args[1:]
		default:
			return fmt.Errorf("unknown command %q (see --help)", args[0])
		}
	}
	if len(args) > 0 {
		return errors.New("stdio does not accept arguments")
	}
	c, err := bridge.DefaultClient()
	if err != nil {
		return err
	}
	err = bridge.NewServer(c, version).Run(ctx, &mcp.StdioTransport{})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func runService(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("service requires ensure, run, status or stop")
	}
	dir, err := service.Directory()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("service "+args[0], flag.ContinueOnError)
	flags.StringVar(&dir, "dir", dir, "Service runtime directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected service arguments")
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	switch args[0] {
	case "run":
		l, d, cleanup, err := service.Listen(dir)
		if err != nil {
			return err
		}
		defer cleanup()
		return service.NewHub(d.Token).Serve(ctx, l)
	case "ensure":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		startCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		d, err := service.Ensure(startCtx, dir, exe)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(d)
	case "status", "stop":
		d, err := service.ReadDescriptor(dir)
		if err != nil {
			return err
		}
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		c, err := service.Connect(callCtx, d)
		if err != nil {
			return err
		}
		defer c.Close()
		if args[0] == "stop" {
			return c.Call(callCtx, "service/stop", nil, nil)
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"pid": d.PID, "endpoint": d.Endpoint})
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
}

func install(args []string) error {
	dir := os.Getenv("TTT_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".config", "ttt")
	}
	dir = filepath.Join(dir, "plugins", "ttt-mcp")
	flags := flag.NewFlagSet("install-plugin", flag.ContinueOnError)
	flags.StringVar(&dir, "dir", dir, "Destination plugin directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected install arguments")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for _, name := range []string{"init.lua", "plugin.ttt.json"} {
		data, err := plugin.Files.ReadFile(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			return err
		}
	}
	fmt.Println("Installed ttt-mcp plugin to", dir)
	return nil
}

const usage = `ttt-mcp — discover and control running ttt editors through MCP

Usage:
  ttt-mcp                         Serve MCP over stdio (agent entry point)
  ttt-mcp sessions                List connected editor sessions as JSON
  ttt-mcp install-plugin [--dir PATH]
  ttt-mcp service ensure          Start or reuse the shared service
  ttt-mcp service run             Run the shared service in foreground
  ttt-mcp service status          Show service PID and endpoint
  ttt-mcp service stop            Stop the service
  ttt-mcp version

TTT_MCP_DIR overrides ~/.cache/ttt-mcp for the service and all clients.
TTT_CONFIG_DIR overrides the ttt configuration directory when installing.
The Lua plugin starts the service automatically. Requires ttt's RPC and
automation plugin APIs; see README for the development branch.
`
