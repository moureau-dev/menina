package proxy

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

type RouteTimeouts struct {
	Read  string `json:"read,omitempty"`
	Write string `json:"write,omitempty"`
	Idle  string `json:"idle,omitempty"`
}

type Route struct {
	Host         string            `json:"host"`
	PathPrefix   string            `json:"pathPrefix,omitempty"`
	Backend      string            `json:"backend"`
	Service      string            `json:"service"`
	Namespace    string            `json:"namespace"`
	Project      string            `json:"project"`
	Environment  string            `json:"environment"`
	App          string            `json:"app"`
	TLS          bool              `json:"tls"`
	RedirectHTTP bool              `json:"redirectHttp"`
	Compression  bool              `json:"compression"`
	H2C          bool              `json:"h2c"`
	Streaming    bool              `json:"streaming"`
	MaxBodyBytes int64             `json:"maxBodyBytes,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Timeouts     RouteTimeouts     `json:"timeouts,omitempty"`
}

type Snapshot struct {
	Version     int64     `json:"version"`
	GeneratedAt time.Time `json:"generatedAt"`
	Routes      []Route   `json:"routes"`
}

type Table struct {
	version int64
	byHost  map[string][]Route
	count   int
}

type RoutingTable struct {
	current atomic.Pointer[Table]
}

func NewRoutingTable() *RoutingTable {
	rt := &RoutingTable{}
	rt.current.Store(&Table{byHost: map[string][]Route{}})
	return rt
}

func (rt *RoutingTable) Validate(s *Snapshot) error {
	if s == nil {
		return fmt.Errorf("snapshot is nil")
	}
	seen := map[string]string{}
	for i, r := range s.Routes {
		if r.Host == "" {
			return fmt.Errorf("route %d has empty host", i)
		}
		if r.Backend == "" {
			return fmt.Errorf("route %d (%s) has empty backend", i, r.Host)
		}
		if _, _, err := net.SplitHostPort(r.Backend); err != nil {
			return fmt.Errorf("route %d (%s) has invalid backend %q: %w", i, r.Host, r.Backend, err)
		}
		prefix := r.PathPrefix
		if prefix == "" {
			prefix = "/"
		}
		key := strings.ToLower(r.Host) + "\x00" + prefix
		if owner, ok := seen[key]; ok {
			return fmt.Errorf("duplicate route %s%s (also claimed by %s)", r.Host, prefix, owner)
		}
		seen[key] = r.Host
	}
	return nil
}

func (rt *RoutingTable) Swap(s *Snapshot) error {
	if err := rt.Validate(s); err != nil {
		return err
	}
	t := &Table{version: s.Version, byHost: map[string][]Route{}, count: len(s.Routes)}
	for _, r := range s.Routes {
		host := strings.ToLower(strings.TrimSpace(r.Host))
		prefix := r.PathPrefix
		if prefix == "" {
			prefix = "/"
		}
		r.Host = host
		r.PathPrefix = prefix
		t.byHost[host] = append(t.byHost[host], r)
	}
	for _, routes := range t.byHost {
		sort.SliceStable(routes, func(i, j int) bool {
			return len(routes[i].PathPrefix) > len(routes[j].PathPrefix)
		})
	}
	rt.current.Store(t)
	return nil
}

func (rt *RoutingTable) Lookup(host, path string) (Route, bool) {
	t := rt.current.Load()
	if t == nil {
		return Route{}, false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	routes := t.byHost[host]
	for _, r := range routes {
		if strings.HasPrefix(path, r.PathPrefix) {
			return r, true
		}
	}
	return Route{}, false
}

func (rt *RoutingTable) Version() int64 {
	t := rt.current.Load()
	if t == nil {
		return 0
	}
	return t.version
}

func (rt *RoutingTable) HasHost(host string) bool {
	t := rt.current.Load()
	if t == nil {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	_, ok := t.byHost[host]
	return ok
}

func (rt *RoutingTable) Hosts() []string {
	t := rt.current.Load()
	if t == nil {
		return nil
	}
	out := make([]string, 0, len(t.byHost))
	for host := range t.byHost {
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

func (rt *RoutingTable) Count() int {
	t := rt.current.Load()
	if t == nil {
		return 0
	}
	return t.count
}
