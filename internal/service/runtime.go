package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/eugenioenko/ttt-mcp/internal/rpc"
	"github.com/gofrs/flock"
)

type Descriptor struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
	PID      int    `json:"pid"`
}

func Directory() (string, error) {
	if dir := os.Getenv("TTT_MCP_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "ttt-mcp"), nil
}

func prepare(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("service directory must be a real directory")
	}
	return os.Chmod(dir, 0700)
}

func ReadDescriptor(dir string) (Descriptor, error) {
	var d Descriptor
	data, err := os.ReadFile(filepath.Join(dir, "service.json"))
	if err != nil {
		return d, err
	}
	err = json.Unmarshal(data, &d)
	if err == nil && (d.Token == "" || d.Endpoint == "") {
		err = errors.New("invalid service descriptor")
	}
	return d, err
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	network, address := "tcp", endpoint
	if path, ok := strings.CutPrefix(endpoint, "unix:"); ok {
		network, address = "unix", path
	} else {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("service endpoint must be local")
		}
	}
	return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
}

func Connect(ctx context.Context, d Descriptor) (*rpc.Conn, error) {
	n, err := dial(ctx, d.Endpoint)
	if err != nil {
		return nil, err
	}
	c := rpc.New(n, nil)
	if err := c.Call(ctx, "hello", map[string]any{"role": "bridge", "token": d.Token}, nil); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func Ensure(ctx context.Context, dir, executable string) (Descriptor, error) {
	if err := prepare(dir); err != nil {
		return Descriptor{}, err
	}
	lock := flock.New(filepath.Join(dir, "start.lock"))
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return Descriptor{}, err
	}
	if !locked {
		return Descriptor{}, ctx.Err()
	}
	defer lock.Close()
	probe := func() (Descriptor, error) {
		d, err := ReadDescriptor(dir)
		if err != nil {
			return d, err
		}
		probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		c, err := Connect(probeCtx, d)
		if err == nil {
			c.Close()
		}
		return d, err
	}
	if d, err := probe(); err == nil {
		return d, nil
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return Descriptor{}, err
	}
	defer logFile.Close()
	cmd := exec.Command(executable, "service", "run", "--dir", dir)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return Descriptor{}, err
	}
	_ = cmd.Process.Release()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if d, err := probe(); err == nil {
			return d, nil
		}
		select {
		case <-ctx.Done():
			return Descriptor{}, fmt.Errorf("service startup failed (see %s): %w", filepath.Join(dir, "service.log"), ctx.Err())
		case <-ticker.C:
		}
	}
}

func Listen(dir string) (net.Listener, Descriptor, func(), error) {
	if err := prepare(dir); err != nil {
		return nil, Descriptor{}, nil, err
	}
	lock := flock.New(filepath.Join(dir, "service.lock"))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		lock.Close()
		return nil, Descriptor{}, nil, errors.New("service already running")
	}
	network, address := "unix", filepath.Join(dir, "hub.sock")
	if runtime.GOOS == "windows" {
		network, address = "tcp", "127.0.0.1:4251"
	} else {
		if len(address) > 100 {
			lock.Close()
			return nil, Descriptor{}, nil, errors.New("service directory is too long for a Unix socket")
		}
		if err := os.Remove(address); err != nil && !errors.Is(err, os.ErrNotExist) {
			lock.Close()
			return nil, Descriptor{}, nil, err
		}
	}
	l, err := net.Listen(network, address)
	if err != nil {
		lock.Close()
		return nil, Descriptor{}, nil, err
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		l.Close()
		lock.Close()
		return nil, Descriptor{}, nil, err
	}
	d := Descriptor{Endpoint: l.Addr().String(), Token: hex.EncodeToString(token), PID: os.Getpid()}
	if network == "unix" {
		d.Endpoint = "unix:" + address
		_ = os.Chmod(address, 0600)
	}
	data, _ := json.Marshal(d)
	path := filepath.Join(dir, "service.json")
	if err = os.WriteFile(path+".tmp", data, 0600); err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		l.Close()
		lock.Close()
		return nil, Descriptor{}, nil, err
	}
	cleanup := func() {
		l.Close()
		_ = os.Remove(path)
		if network == "unix" {
			_ = os.Remove(address)
		}
		lock.Close()
	}
	return l, d, cleanup, nil
}
