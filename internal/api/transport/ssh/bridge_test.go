package ssh

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeCaller struct{}

func (fakeCaller) Request(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	return 200, []byte(`{"method":"` + method + `","path":"` + path + `"}`), nil
}

func TestServeRoundTrip(t *testing.T) {
	in := strings.NewReader(`{"method":"GET","path":"/jobs"}`)
	var out bytes.Buffer
	if err := Serve(context.Background(), fakeCaller{}, in, &out); err != nil {
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
	if body["path"] != "/jobs" || body["method"] != "GET" {
		t.Fatalf("body = %+v", body)
	}
}
