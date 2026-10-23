// Package ssh bridges stdin JSON requests to the local API for use as an SSH
// forced command.
package ssh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	apierrors "github.com/moureau-dev/menina/internal/api/errors"
)

// Request is one API call received over the SSH bridge.
type Request struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// Response is the JSON result written back to the SSH client.
type Response struct {
	Status int             `json:"status,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a classified API error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Caller performs one API request.
type Caller interface {
	Request(ctx context.Context, method, path string, body []byte) (int, []byte, error)
}

// Serve reads one request, calls the API, and writes one response.
func Serve(ctx context.Context, client Caller, in io.Reader, out io.Writer) error {
	var req Request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	if req.Path == "" {
		return fmt.Errorf("path is required")
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	status, body, err := client.Request(ctx, req.Method, req.Path, req.Body)
	resp := Response{Status: status}
	if len(body) > 0 {
		if json.Valid(body) {
			resp.Body = json.RawMessage(body)
		} else {
			encoded, _ := json.Marshal(string(body))
			resp.Body = encoded
		}
	}
	if err != nil {
		code, message, hint, _ := apierrors.Info(err)
		resp.Error = &Error{Code: code, Message: message, Hint: hint}
	}
	if encodeErr := json.NewEncoder(out).Encode(resp); encodeErr != nil {
		return encodeErr
	}
	return err
}
