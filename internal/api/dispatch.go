package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	apierrors "github.com/moureau-dev/menina/internal/api/errors"
)

// Version is the API command contract version.
const Version = 1

// Actor is the server-derived caller identity.
type Actor struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

type actorKey struct{}

// WithActor stores an actor in the context.
func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFromContext returns the server-derived actor.
func ActorFromContext(ctx context.Context) Actor {
	actor, _ := ctx.Value(actorKey{}).(Actor)
	return actor
}

// Request is one versioned API command.
type Request struct {
	Version        int             `json:"version"`
	Method         string          `json:"method,omitempty"`
	Path           string          `json:"path"`
	Body           json.RawMessage `json:"body,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
}

// Response is one versioned API result.
type Response struct {
	Version int             `json:"version"`
	Status  int             `json:"status"`
	Body    json.RawMessage `json:"body,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a classified API error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type cachedResponse struct {
	response Response
	expires  time.Time
}

// Dispatcher validates the command envelope and calls the HTTP command mux.
// Legacy routes pass through for compatibility.
type Dispatcher struct {
	handler    http.Handler
	authorizer Authorizer
	mu         sync.Mutex
	cache      map[string]cachedResponse
	ttl        time.Duration
	now        func() time.Time
}

// NewDispatcher builds a dispatcher around an existing command mux.
func NewDispatcher(handler http.Handler) *Dispatcher {
	return &Dispatcher{handler: handler, cache: map[string]cachedResponse{}, ttl: 24 * time.Hour, now: time.Now}
}

// SetAuthorizer installs the scope enforcement policy.
func (d *Dispatcher) SetAuthorizer(authorizer Authorizer) { d.authorizer = authorizer }

// Do executes one request and returns a versioned response.
func (d *Dispatcher) Do(ctx context.Context, actor Actor, req Request) Response {
	if req.Version == 0 {
		req.Version = Version
	}
	if req.Version != Version {
		return errorResponse(http.StatusBadRequest, apierrors.CodeUnsupported, fmt.Sprintf("unsupported API version %d", req.Version), fmt.Sprintf("use version %d", Version))
	}
	if req.Path == "" {
		return errorResponse(http.StatusBadRequest, apierrors.CodeValidation, "path is required", "")
	}
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	if d.authorizer != nil {
		if err := d.authorizer.Authorize(ctx, actor, req); err != nil {
			return authorizeError(err)
		}
	}

	key := ""
	if req.IdempotencyKey != "" && idempotentMethod(req.Method) {
		key = actor.ID + "\x00" + req.IdempotencyKey
		if cached, ok := d.lookup(key); ok {
			return cached
		}
	}

	httpReq, err := http.NewRequestWithContext(WithActor(ctx, actor), req.Method, "http://api"+req.Path, bytes.NewReader(req.Body))
	if err != nil {
		return errorResponse(http.StatusBadRequest, apierrors.CodeValidation, err.Error(), "")
	}
	httpReq.Header.Set("X-Menina-API-Version", fmt.Sprint(Version))
	rec := &recorder{header: http.Header{}}
	d.handler.ServeHTTP(rec, httpReq)

	resp := Response{Version: Version, Status: rec.statusCode(), Body: json.RawMessage(rec.body.Bytes())}
	if resp.Status >= 400 {
		code, message, hint, _ := apierrors.Info(fmt.Errorf("%s", string(resp.Body)))
		if code == apierrors.CodeGeneric && len(resp.Body) > 0 {
			message = string(resp.Body)
		}
		if code != apierrors.CodeGeneric || len(resp.Body) > 0 {
			resp.Error = &Error{Code: code, Message: message, Hint: hint}
		}
	}
	if key != "" && resp.Status < 500 {
		d.store(key, resp)
	}
	return resp
}

// ServeHTTP serves the versioned command envelope at /v1/commands and passes
// legacy routes through unchanged.
func (d *Dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actor := Actor{ID: "local", Source: "http"}
	if isLocalRequest(r) && r.Header.Get("X-Menina-Actor-ID") != "" {
		actor = Actor{ID: r.Header.Get("X-Menina-Actor-ID"), Source: r.Header.Get("X-Menina-Actor-Source")}
	}
	if r.URL.Path != "/v1/commands" {
		d.handler.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req Request
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := d.Do(r.Context(), actor, req)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Menina-API-Version", fmt.Sprint(Version))
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func isLocalRequest(r *http.Request) bool {
	return r.RemoteAddr == "" || strings.HasPrefix(r.RemoteAddr, "@")
}

func idempotentMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func (d *Dispatcher) lookup(key string) (Response, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cached, ok := d.cache[key]
	if !ok {
		return Response{}, false
	}
	if d.now().After(cached.expires) {
		delete(d.cache, key)
		return Response{}, false
	}
	return cached.response, true
}

func (d *Dispatcher) store(key string, resp Response) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cache[key] = cachedResponse{response: resp, expires: d.now().Add(d.ttl)}
}

func errorResponse(status int, code, message, hint string) Response {
	return Response{Version: Version, Status: status, Error: &Error{Code: code, Message: message, Hint: hint}}
}

type recorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}

func (r *recorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(data)
}

func (r *recorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
