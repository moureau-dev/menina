package deployment

import (
	"context"
	"encoding/base64"
	"testing"

	"gopkg.in/yaml.v3"
)

func seedTokenSecret(f *fakeAPI) {
	f.seed("/api/v1/namespaces/menina-system/secrets", "menina-token", map[string]any{
		"metadata": map[string]any{"name": "menina-token", "namespace": "menina-system"},
		"type":     "kubernetes.io/service-account-token",
		"data": map[string]any{
			"token":  base64.StdEncoding.EncodeToString([]byte("sa-token")),
			"ca.crt": base64.StdEncoding.EncodeToString([]byte("ca-data")),
		},
	})
}

func TestEnsureServiceAccessBuildsScopedKubeconfig(t *testing.T) {
	f := newFakeAPI(t)
	seedTokenSecret(f)
	client, err := newFakeClient(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := EnsureServiceAccess(context.Background(), client, "menina-system", "menina")
	if err != nil {
		t.Fatalf("EnsureServiceAccess: %v", err)
	}
	var cfg kubeconfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("returned kubeconfig is invalid: %v", err)
	}
	if len(cfg.Users) != 1 || cfg.Users[0].User.Token != "sa-token" {
		t.Fatalf("kubeconfig must use the service account token: %+v", cfg.Users)
	}
	if len(cfg.Clusters) != 1 || cfg.Clusters[0].Cluster.Server != client.Server() {
		t.Fatalf("server = %+v, want %q", cfg.Clusters, client.Server())
	}
	if cfg.Clusters[0].Cluster.CAData != base64.StdEncoding.EncodeToString([]byte("ca-data")) {
		t.Fatalf("CA = %q", cfg.Clusters[0].Cluster.CAData)
	}
	if f.object("/apis/rbac.authorization.k8s.io/v1/clusterroles", PlatformClusterRoleName) == nil {
		t.Fatal("cluster role not created")
	}
	if f.object("/apis/rbac.authorization.k8s.io/v1/clusterrolebindings", PlatformClusterRoleName) == nil {
		t.Fatal("cluster role binding not created")
	}
	if f.object("/api/v1/namespaces/menina-system/serviceaccounts", "menina") == nil {
		t.Fatal("service account not created")
	}
}

func TestMeninaPolicyRulesAreScoped(t *testing.T) {
	for _, rule := range meninaPolicyRules() {
		for _, v := range rule.Verbs {
			if v == "*" {
				t.Fatalf("policy must not grant wildcard verbs: %+v", rule)
			}
		}
		for _, r := range rule.Resources {
			if r == "*" {
				t.Fatalf("policy must not grant wildcard resources: %+v", rule)
			}
			if r == "nodes" {
				for _, v := range rule.Verbs {
					if v != "get" && v != "list" {
						t.Fatalf("nodes access must be read-only, got %v", rule.Verbs)
					}
				}
			}
		}
	}
}
