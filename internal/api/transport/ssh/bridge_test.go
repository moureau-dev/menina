package ssh

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/api"
)

type fakeCaller struct{ actor api.Actor }

func (f fakeCaller) CommandAs(ctx context.Context, actor api.Actor, req api.Request) (api.Response, error) {
	f.actor = actor
	body, _ := json.Marshal(map[string]string{"method": req.Method, "path": req.Path, "actor": actor.ID})
	return api.Response{Version: api.Version, Status: 200, Body: body}, nil
}

func TestServeRoundTrip(t *testing.T) {
	in := strings.NewReader(`{"method":"GET","path":"/jobs"}`)
	var out bytes.Buffer
	if err := Serve(context.Background(), fakeCaller{}, api.Actor{ID: "profile-1", Source: "ssh"}, in, &out); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d", resp.Status)
	}
	var body map[string]string
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["path"] != "/jobs" || body["method"] != "GET" || body["actor"] != "profile-1" {
		t.Fatalf("body = %+v", body)
	}
}
