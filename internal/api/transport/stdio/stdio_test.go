package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/api"
)

func TestServe(t *testing.T) {
	d := api.NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	in := strings.NewReader(`{"version":1,"method":"GET","path":"/status"}` + "\n")
	var out bytes.Buffer
	if err := Serve(context.Background(), d, api.Actor{ID: "stdio", Source: "stdio"}, in, &out); err != nil {
		t.Fatal(err)
	}
	var resp api.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusOK || string(resp.Body) != `{"ok":true}` {
		t.Fatalf("response = %+v", resp)
	}
}
