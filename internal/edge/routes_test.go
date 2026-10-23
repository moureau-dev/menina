package edge

import "testing"

func TestRoutingTableLookupPrefersLongestPrefix(t *testing.T) {
	rt := NewRoutingTable()
	snap := &Snapshot{Version: 3, Routes: []Route{
		{Host: "example.com", PathPrefix: "/", Backend: "10.0.0.1:80", App: "root"},
		{Host: "example.com", PathPrefix: "/api", Backend: "10.0.0.2:8080", App: "api"},
	}}
	if err := rt.Swap(snap); err != nil {
		t.Fatal(err)
	}
	if rt.Version() != 3 || rt.Count() != 2 {
		t.Fatalf("version=%d count=%d", rt.Version(), rt.Count())
	}
	r, ok := rt.Lookup("example.com", "/api/users")
	if !ok || r.App != "api" {
		t.Fatalf("expected api route, got %+v ok=%v", r, ok)
	}
	r, ok = rt.Lookup("example.com", "/other")
	if !ok || r.App != "root" {
		t.Fatalf("expected root route, got %+v ok=%v", r, ok)
	}
	if _, ok := rt.Lookup("unknown.com", "/"); ok {
		t.Fatal("expected no route for unknown host")
	}
}

func TestRoutingTableStripsHostPort(t *testing.T) {
	rt := NewRoutingTable()
	_ = rt.Swap(&Snapshot{Routes: []Route{{Host: "Example.COM", PathPrefix: "/", Backend: "1.2.3.4:80"}}})
	if _, ok := rt.Lookup("example.com:443", "/"); !ok {
		t.Fatal("expected host:port to match")
	}
}

func TestRoutingTableRejectsDuplicate(t *testing.T) {
	rt := NewRoutingTable()
	err := rt.Swap(&Snapshot{Routes: []Route{
		{Host: "a.com", PathPrefix: "/", Backend: "1.1.1.1:80"},
		{Host: "a.com", PathPrefix: "/", Backend: "2.2.2.2:80"},
	}})
	if err == nil {
		t.Fatal("expected duplicate route validation error")
	}
}

func TestRoutingTableRejectsBadBackend(t *testing.T) {
	rt := NewRoutingTable()
	if err := rt.Swap(&Snapshot{Routes: []Route{{Host: "a.com", Backend: "no-port"}}}); err == nil {
		t.Fatal("expected invalid backend error")
	}
}

func TestRoutingTableAtomicSwapKeepsOldOnError(t *testing.T) {
	rt := NewRoutingTable()
	_ = rt.Swap(&Snapshot{Version: 1, Routes: []Route{{Host: "a.com", PathPrefix: "/", Backend: "1.1.1.1:80"}}})
	if err := rt.Swap(&Snapshot{Version: 2, Routes: []Route{{Host: "b.com", PathPrefix: "/", Backend: "bad"}}}); err == nil {
		t.Fatal("expected error")
	}
	if rt.Version() != 1 {
		t.Fatalf("failed swap should not replace table, version=%d", rt.Version())
	}
}
