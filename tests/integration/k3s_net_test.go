//go:build integration

// This test validates the host(Menina proxy netns) -> k8s ClusterIP path.
// It MUST run inside the k3s node's network namespace (i.e. on the host where
// k3s and Menina's proxy both run), because that is the supported deployment.
// Run it via scripts/k3s-net-e2e.sh which executes it inside the k3s node.
package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/edge"
)

func TestHostToClusterIPEndToEnd(t *testing.T) {
	if os.Getenv("MENINA_K3S_E2E") == "" {
		t.Skip("set MENINA_K3S_E2E=1 (run via scripts/k3s-net-e2e.sh) to exercise real k3s routing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cluster, err := deployment.NewKubernetesCluster(os.Getenv("KUBECONFIG"))
	if err != nil {
		t.Fatalf("kubeconfig: %v", err)
	}
	if err := cluster.Ping(ctx); err != nil {
		t.Fatalf("kubernetes API: %v", err)
	}

	ns := "menina-e2e"
	if err := cluster.EnsureNamespace(ctx, ns); err != nil {
		t.Fatal(err)
	}
	w := deployment.Workload{
		Project: "e2e", Environment: "test", App: "web", Name: "web", Namespace: ns,
		Image: "nginx:alpine", Port: 80, ContainerPort: 80, Replicas: 1, Type: "service",
	}
	if err := cluster.ApplyService(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := cluster.ApplyWorkload(ctx, w); err != nil {
		t.Fatal(err)
	}
	if _, err := deployment.WaitForRollout(ctx, cluster, ns, "web", deployment.RolloutOptions{Timeout: 3 * time.Minute}); err != nil {
		t.Fatalf("initial rollout: %v", err)
	}

	ip, err := cluster.ServiceClusterIP(ctx, ns, "web")
	if err != nil || ip == "" {
		t.Fatalf("clusterIP=%q err=%v", ip, err)
	}
	t.Logf("web ClusterIP = %s", ip)

	server, err := edge.NewServer(edge.Options{Log: log})
	if err != nil {
		t.Fatal(err)
	}
	routeHost := "e2e.local"
	backend := fmt.Sprintf("%s:80", ip)
	if err := server.UpdateSnapshot(&edge.Snapshot{Version: 1, Routes: []edge.Route{{Host: routeHost, PathPrefix: "/", Backend: backend}}}); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(server.Handler())
	defer front.Close()

	// 1. host -> ClusterIP via the proxy works.
	mustProxyGET(t, front.URL, routeHost)

	// 2. Rollout (new pods) must keep working with the SAME ClusterIP and no
	//    route change. During the rollout there may be a brief window with no
	//    ready endpoints (connection refused -> 502); it must recover.
	if err := cluster.RestartWorkload(ctx, ns, "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := deployment.WaitForRollout(ctx, cluster, ns, "web", deployment.RolloutOptions{Timeout: 3 * time.Minute}); err != nil {
		t.Fatalf("rollout after restart: %v", err)
	}
	ip2, _ := cluster.ServiceClusterIP(ctx, ns, "web")
	if ip2 != ip {
		t.Fatalf("ClusterIP changed across rollout: %s -> %s", ip, ip2)
	}
	eventuallyProxyGET(t, front.URL, routeHost, 90*time.Second)

	// 3. A proxy restart (same snapshot) must keep working.
	front2 := httptest.NewServer(server.Handler())
	defer front2.Close()
	eventuallyProxyGET(t, front2.URL, routeHost, 90*time.Second)
}

func mustProxyGET(t *testing.T, base, host string) {
	t.Helper()
	if err := proxyGET(base, host); err != nil {
		t.Fatalf("proxy request: %v", err)
	}
}

func eventuallyProxyGET(t *testing.T, base, host string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if err := proxyGET(base, host); err == nil {
			return
		} else {
			last = err
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("proxy never recovered within %s: %v", timeout, last)
}

func proxyGET(base, host string) error {
	req, err := http.NewRequest(http.MethodGet, base+"/", nil)
	if err != nil {
		return err
	}
	req.Host = host
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
