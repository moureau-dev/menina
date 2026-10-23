//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/moureau-dev/menina/internal/core/cluster"
	"github.com/moureau-dev/menina/internal/core/cluster/kube"
)

func TestProvisionedServiceIdentityIsScoped(t *testing.T) {
	if os.Getenv("MENINA_K3S_E2E") == "" {
		t.Skip("set MENINA_K3S_E2E=1 (run via scripts/rbac-e2e.sh) to exercise real RBAC")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	data, err := k8s.ProvisionServiceAccess(ctx, os.Getenv("KUBECONFIG"), "menina-system", "menina")
	if err != nil {
		t.Fatalf("ProvisionServiceAccess: %v", err)
	}
	cfg, err := kube.Parse(data)
	if err != nil {
		t.Fatalf("returned kubeconfig is invalid: %v", err)
	}
	client, err := kube.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Version(ctx); err != nil {
		t.Fatalf("service identity cannot reach the API: %v", err)
	}

	assertCan(t, client, true, "list", "", "pods", "")
	assertCan(t, client, true, "get", "", "nodes", "")
	assertCan(t, client, true, "create", "apps", "deployments", "menina-apps")
	assertCan(t, client, true, "create", "autoscaling", "horizontalpodautoscalers", "menina-apps")
	assertCan(t, client, true, "create", "", "namespaces", "")
	assertCan(t, client, false, "delete", "", "nodes", "")
	assertCan(t, client, false, "delete", "", "namespaces", "")
	assertCan(t, client, false, "create", "rbac.authorization.k8s.io", "clusterroles", "")
}

func assertCan(t *testing.T, client *kube.Client, want bool, verb, group, resource, namespace string) {
	t.Helper()
	review := &kube.SelfSubjectAccessReview{
		TypeMeta: kube.TypeMeta{APIVersion: "authorization.k8s.io/v1", Kind: "SelfSubjectAccessReview"},
		Spec: kube.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &kube.ResourceAttributes{Verb: verb, Group: group, Resource: resource, Namespace: namespace},
		},
	}
	var out kube.SelfSubjectAccessReview
	if err := client.Create(context.Background(), "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", review, &out); err != nil {
		t.Fatalf("self subject access review: %v", err)
	}
	if out.Status.Allowed != want {
		t.Fatalf("%s %s/%s: allowed=%v want=%v (reason: %s)", verb, group, resource, out.Status.Allowed, want, out.Status.Reason)
	}
}
