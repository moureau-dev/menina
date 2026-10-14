package platform

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moureau-dev/menina/internal/proxy"
)

func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func EnsureToken(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		return strings.TrimSpace(string(data)), nil
	}
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token), 0o640); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o640); err != nil {
		return "", err
	}
	return token, nil
}

type ControlServer struct {
	socketPath string
	token      string
	handler    http.Handler
	ln         net.Listener
	srv        *http.Server
}

func NewControlServer(socketPath, token string, handler http.Handler) (*ControlServer, error) {
	return &ControlServer{socketPath: socketPath, token: token, handler: handler}, nil
}

func (c *ControlServer) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(c.socketPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(c.socketPath)
	ln, err := net.Listen("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("listen on control socket: %w", err)
	}
	if err := os.Chmod(c.socketPath, 0o660); err != nil {
		return err
	}
	c.ln = ln
	c.srv = &http.Server{Handler: c.withAuth(c.handler)}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.srv.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := c.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "control server: %v\n", err)
		}
	}()
	return nil
}

func (c *ControlServer) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(c.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type ControlClient struct {
	socketPath string
	token      string
	http       *http.Client
}

func DialControl(socketPath, tokenPath string) (*ControlClient, error) {
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read control token: %w", err)
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &ControlClient{
		socketPath: socketPath,
		token:      strings.TrimSpace(string(token)),
		http:       &http.Client{Transport: tr, Timeout: 10 * time.Second},
	}, nil
}

func (c *ControlClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return c.http.Do(req)
}

func (c *ControlClient) GetJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("control request %s failed: %s: %s", path, resp.Status, strings.TrimSpace(string(data)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *ControlClient) Snapshot(ctx context.Context) (*proxy.Snapshot, error) {
	var snap proxy.Snapshot
	if err := c.GetJSON(ctx, "/snapshot", &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (c *ControlClient) PostJSON(ctx context.Context, path string, body []byte) error {
	resp, err := c.do(ctx, http.MethodPost, path, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("control request %s failed: %s: %s", path, resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

// PostJSONDecode posts a JSON body and decodes the JSON response.
func (c *ControlClient) PostJSONDecode(ctx context.Context, path string, body []byte, out any) error {
	resp, err := c.do(ctx, http.MethodPost, path, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("control request %s failed: %s: %s", path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type SnapshotStream func(*proxy.Snapshot)

func (c *ControlClient) Stream(ctx context.Context, fn SnapshotStream) error {
	resp, err := c.do(ctx, http.MethodGet, "/stream", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stream failed: %s", resp.Status)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var snap proxy.Snapshot
		if err := json.Unmarshal(line, &snap); err != nil {
			continue
		}
		fn(&snap)
	}
	return scanner.Err()
}
