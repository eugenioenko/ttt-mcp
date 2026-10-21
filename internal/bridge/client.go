package bridge

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/eugenioenko/ttt-mcp/internal/service"
)

type Client struct {
	Directory  string
	Executable string
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	startCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	d, err := service.Ensure(startCtx, c.Directory, c.Executable)
	if err != nil {
		cancel()
		return err
	}
	conn, err := service.Connect(startCtx, d)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Call(ctx, method, params, result)
}

func DefaultClient() (*Client, error) {
	dir, err := service.Directory()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Client{Directory: dir, Executable: exe}, nil
}

func (c *Client) Run(ctx context.Context, id string, actions []service.Action) (any, error) {
	var result json.RawMessage
	err := c.Call(ctx, "sessions/call", map[string]any{"session_id": id, "actions": actions}, &result)
	if err != nil {
		return nil, err
	}
	var value any
	err = json.Unmarshal(result, &value)
	return value, err
}
