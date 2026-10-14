package deployment

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/moureau-dev/menina/internal/kube"
)

const (
	PlatformClusterRoleName = "menina-platform"
	platformServiceAccount  = "menina"
	platformContextName     = "menina"
)

var accessTokenPollTimeout = 30 * time.Second

// ProvisionServiceAccess creates (or reconciles) the Menina service identity on
// a cluster reachable via the given (admin-capable) kubeconfig, and returns a
// kubeconfig that uses only that scoped identity.
func ProvisionServiceAccess(ctx context.Context, kubeconfig, namespace, saName string) ([]byte, error) {
	cfg, err := kube.Load(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig for RBAC provisioning: %w", err)
	}
	client, err := kube.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return EnsureServiceAccess(ctx, client, namespace, saName)
}

func EnsureServiceAccess(ctx context.Context, client *kube.Client, namespace, saName string) ([]byte, error) {
	if namespace == "" {
		namespace = "menina-system"
	}
	if saName == "" {
		saName = platformServiceAccount
	}
	if err := ensureNamespace(ctx, client, namespace); err != nil {
		return nil, err
	}
	if err := ensureServiceAccount(ctx, client, namespace, saName); err != nil {
		return nil, err
	}
	if err := ensureClusterRole(ctx, client); err != nil {
		return nil, err
	}
	if err := ensureClusterRoleBinding(ctx, client, namespace, saName); err != nil {
		return nil, err
	}
	token, ca, err := ensureServiceAccountToken(ctx, client, namespace, saName)
	if err != nil {
		return nil, err
	}
	if len(ca) == 0 {
		ca = client.CAData()
	}
	return buildServiceKubeconfig(client.Server(), ca, token)
}

func meninaPolicyRules() []kube.PolicyRule {
	return []kube.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: []string{"get", "list", "create"}},
		{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get", "list"}},
		{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
		{APIGroups: []string{""}, Resources: []string{"configmaps", "secrets", "persistentvolumeclaims"}, Verbs: []string{"get", "list", "watch", "create", "update"}},
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{""}, Resources: []string{"pods/log"}, Verbs: []string{"get"}},
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
	}
}

func ensureNamespace(ctx context.Context, client *kube.Client, name string) error {
	if err := client.Get(ctx, coreV1+"/namespaces/"+name, &kube.Namespace{}); err == nil {
		return nil
	} else if !kube.IsNotFound(err) {
		return err
	}
	err := client.Create(ctx, coreV1+"/namespaces", &kube.Namespace{
		TypeMeta: kube.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		Metadata: kube.ObjectMeta{Name: name, Labels: map[string]string{labelManagedBy: managedByValue}},
	}, nil)
	if kube.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func ensureServiceAccount(ctx context.Context, client *kube.Client, namespace, name string) error {
	if err := client.Get(ctx, nsResource(coreV1, namespace, "serviceaccounts")+"/"+name, &kube.ServiceAccount{}); err == nil {
		return nil
	} else if !kube.IsNotFound(err) {
		return err
	}
	err := client.Create(ctx, nsResource(coreV1, namespace, "serviceaccounts"), &kube.ServiceAccount{
		TypeMeta: kube.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		Metadata: kube.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{labelManagedBy: managedByValue}},
	}, nil)
	if kube.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func ensureClusterRole(ctx context.Context, client *kube.Client) error {
	item := rbacV1 + "/clusterroles/" + PlatformClusterRoleName
	var existing kube.ClusterRole
	err := client.Get(ctx, item, &existing)
	if kube.IsNotFound(err) {
		return client.Create(ctx, rbacV1+"/clusterroles", &kube.ClusterRole{
			TypeMeta: kube.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
			Metadata: kube.ObjectMeta{Name: PlatformClusterRoleName, Labels: map[string]string{labelManagedBy: managedByValue}},
			Rules:    meninaPolicyRules(),
		}, nil)
	}
	if err != nil {
		return err
	}
	existing.Rules = meninaPolicyRules()
	return client.Update(ctx, item, &existing, nil)
}

func ensureClusterRoleBinding(ctx context.Context, client *kube.Client, namespace, saName string) error {
	item := rbacV1 + "/clusterrolebindings/" + PlatformClusterRoleName
	desired := kube.ClusterRoleBinding{
		TypeMeta: kube.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
		Metadata: kube.ObjectMeta{Name: PlatformClusterRoleName, Labels: map[string]string{labelManagedBy: managedByValue}},
		RoleRef:  kube.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: PlatformClusterRoleName},
		Subjects: []kube.Subject{{Kind: "ServiceAccount", Name: saName, Namespace: namespace}},
	}
	var existing kube.ClusterRoleBinding
	err := client.Get(ctx, item, &existing)
	if kube.IsNotFound(err) {
		return client.Create(ctx, rbacV1+"/clusterrolebindings", &desired, nil)
	}
	if err != nil {
		return err
	}
	existing.RoleRef = desired.RoleRef
	existing.Subjects = desired.Subjects
	return client.Update(ctx, item, &existing, nil)
}

func ensureServiceAccountToken(ctx context.Context, client *kube.Client, namespace, saName string) (token, ca []byte, err error) {
	secretName := saName + "-token"
	collection := nsResource(coreV1, namespace, "secrets")
	item := collection + "/" + secretName
	deadline := time.Now().Add(accessTokenPollTimeout)
	for {
		var secret kube.Secret
		getErr := client.Get(ctx, item, &secret)
		if kube.IsNotFound(getErr) {
			createErr := client.Create(ctx, collection, &kube.Secret{
				TypeMeta: kube.TypeMeta{APIVersion: "v1", Kind: "Secret"},
				Metadata: kube.ObjectMeta{
					Name:        secretName,
					Namespace:   namespace,
					Labels:      map[string]string{labelManagedBy: managedByValue},
					Annotations: map[string]string{"kubernetes.io/service-account.name": saName},
				},
				Type: "kubernetes.io/service-account-token",
			}, nil)
			if createErr != nil && !kube.IsAlreadyExists(createErr) {
				return nil, nil, fmt.Errorf("create service account token secret: %w", createErr)
			}
		} else if getErr != nil {
			return nil, nil, getErr
		} else if len(secret.Data["token"]) > 0 {
			return secret.Data["token"], secret.Data["ca.crt"], nil
		}
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("service account token for %s/%s was not populated within %s", namespace, saName, accessTokenPollTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

type kubeconfigFile struct {
	APIVersion     string              `yaml:"apiVersion"`
	Kind           string              `yaml:"kind"`
	Clusters       []kubeconfigCluster `yaml:"clusters"`
	Contexts       []kubeconfigContext `yaml:"contexts"`
	Users          []kubeconfigUser    `yaml:"users"`
	CurrentContext string              `yaml:"current-context"`
}

type kubeconfigCluster struct {
	Name    string `yaml:"name"`
	Cluster struct {
		Server   string `yaml:"server"`
		CAData   string `yaml:"certificate-authority-data,omitempty"`
		Insecure bool   `yaml:"insecure-skip-tls-verify,omitempty"`
	} `yaml:"cluster"`
}

type kubeconfigContext struct {
	Name    string `yaml:"name"`
	Context struct {
		Cluster   string `yaml:"cluster"`
		User      string `yaml:"user"`
		Namespace string `yaml:"namespace,omitempty"`
	} `yaml:"context"`
}

type kubeconfigUser struct {
	Name string `yaml:"name"`
	User struct {
		Token string `yaml:"token"`
	} `yaml:"user"`
}

func buildServiceKubeconfig(server string, ca, token []byte) ([]byte, error) {
	cfg := kubeconfigFile{
		APIVersion:     "v1",
		Kind:           "Config",
		CurrentContext: platformContextName,
	}
	var cluster kubeconfigCluster
	cluster.Name = platformContextName
	cluster.Cluster.Server = server
	if len(ca) > 0 {
		cluster.Cluster.CAData = base64.StdEncoding.EncodeToString(ca)
	} else {
		cluster.Cluster.Insecure = true
	}
	var ctx kubeconfigContext
	ctx.Name = platformContextName
	ctx.Context.Cluster = platformContextName
	ctx.Context.User = platformContextName
	var user kubeconfigUser
	user.Name = platformContextName
	user.User.Token = string(token)
	cfg.Clusters = []kubeconfigCluster{cluster}
	cfg.Contexts = []kubeconfigContext{ctx}
	cfg.Users = []kubeconfigUser{user}
	return yaml.Marshal(cfg)
}
