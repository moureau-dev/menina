//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/moureau-dev/menina/internal/deployment"
)

func TestProvisionedServiceIdentityIsScoped(t *testing.T) {
	if os.Getenv("MENINA_K3S_E2E") == "" {
		t.Skip("set MENINA_K3S_E2E=1 (run via scripts/rbac-e2e.sh) to exercise real RBAC")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	data, err := deployment.ProvisionServiceAccess(ctx, os.Getenv("KUBECONFIG"), "menina-system", "menina")
	if err != nil {
		t.Fatalf("ProvisionServiceAccess: %v", err)
	}
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		t.Fatalf("returned kubeconfig is invalid: %v", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Discovery().ServerVersion(); err != nil {
		t.Fatalf("service identity cannot reach the API: %v", err)
	}

	assertCan(t, cs, true, "list", "", "pods", "")
	assertCan(t, cs, true, "get", "", "nodes", "")
	assertCan(t, cs, true, "create", "apps", "deployments", "menina-apps")
	assertCan(t, cs, true, "create", "", "namespaces", "")
	assertCan(t, cs, false, "delete", "", "nodes", "")
	assertCan(t, cs, false, "delete", "", "namespaces", "")
	assertCan(t, cs, false, "create", "rbac.authorization.k8s.io", "clusterroles", "")
}

func assertCan(t *testing.T, cs *kubernetes.Clientset, want bool, verb, group, resource, namespace string) {
	t.Helper()
	sar := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{Verb: verb, Group: group, Resource: resource, Namespace: namespace},
		},
	}
	out, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(context.Background(), sar, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("self subject access review: %v", err)
	}
	if out.Status.Allowed != want {
		t.Fatalf("%s %s/%s: allowed=%v want=%v (reason: %s)", verb, group, resource, out.Status.Allowed, want, out.Status.Reason)
	}
}
