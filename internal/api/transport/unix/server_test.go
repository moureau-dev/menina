package unix

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/moureau-dev/menina/internal/api"
)

func TestServerServesWithToken(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	tokenPath := filepath.Join(dir, "control.token")
	token, err := api.EnsureToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(socket, token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	client, err := api.Dial(socket, tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var out map[string]bool
		err = client.GetJSON(ctx, "/x", &out)
		if err == nil {
			if !out["ok"] {
				t.Fatalf("response = %+v", out)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
