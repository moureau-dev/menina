package deployment

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func TestEnsureServiceAccessBuildsScopedKubeconfig(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "menina-token", Namespace: "menina-system"},
		Type:       corev1.SecretTypeServiceAccountToken,
		Data: map[string][]byte{
			corev1.ServiceAccountTokenKey:  []byte("sa-token"),
			corev1.ServiceAccountRootCAKey: []byte("ca-data"),
		},
	})
	restCfg := &rest.Config{Host: "https://k8s.example:6443", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("admin-ca")}}

	data, err := EnsureServiceAccess(context.Background(), cs, restCfg, "menina-system", "menina")
	if err != nil {
		t.Fatalf("EnsureServiceAccess: %v", err)
	}
	cfg, err := clientcmd.Load(data)
	if err != nil {
		t.Fatalf("returned kubeconfig is invalid: %v", err)
	}
	auth := cfg.AuthInfos["menina"]
	if auth == nil || auth.Token != "sa-token" {
		t.Fatalf("kubeconfig must use the service account token, got %+v", auth)
	}
	if len(auth.ClientCertificateData) != 0 || len(auth.ClientKeyData) != 0 {
		t.Fatal("kubeconfig must not embed admin client credentials")
	}
	if cfg.Clusters["menina"].Server != "https://k8s.example:6443" {
		t.Fatalf("server = %q", cfg.Clusters["menina"].Server)
	}
	if string(cfg.Clusters["menina"].CertificateAuthorityData) != "ca-data" {
		t.Fatalf("CA = %q", cfg.Clusters["menina"].CertificateAuthorityData)
	}
	if _, err := cs.RbacV1().ClusterRoles().Get(context.Background(), PlatformClusterRoleName, metav1.GetOptions{}); err != nil {
		t.Fatalf("cluster role not created: %v", err)
	}
	if _, err := cs.RbacV1().ClusterRoleBindings().Get(context.Background(), PlatformClusterRoleName, metav1.GetOptions{}); err != nil {
		t.Fatalf("cluster role binding not created: %v", err)
	}
	if _, err := cs.CoreV1().ServiceAccounts("menina-system").Get(context.Background(), "menina", metav1.GetOptions{}); err != nil {
		t.Fatalf("service account not created: %v", err)
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
