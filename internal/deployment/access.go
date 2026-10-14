package deployment

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	PlatformClusterRoleName = "menina-platform"
	platformServiceAccount  = "menina"
	platformContextName     = "menina"
)

// accessTokenPollTimeout is a variable so tests can shorten it.
var accessTokenPollTimeout = 30 * time.Second

// ProvisionServiceAccess creates (or reconciles) the Menina service identity —
// a ServiceAccount, a scoped ClusterRole, its binding, and a long-lived token —
// and returns a kubeconfig that uses only that identity. It requires an
// admin-capable kubeconfig to create the RBAC objects; it never falls back to
// using the admin kubeconfig as the service credential.
func ProvisionServiceAccess(ctx context.Context, kubeconfig, namespace, saName string) ([]byte, error) {
	restCfg, err := buildRestConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig for RBAC provisioning: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	return EnsureServiceAccess(ctx, cs, restCfg, namespace, saName)
}

func EnsureServiceAccess(ctx context.Context, cs kubernetes.Interface, restCfg *rest.Config, namespace, saName string) ([]byte, error) {
	if namespace == "" {
		namespace = "menina-system"
	}
	if saName == "" {
		saName = platformServiceAccount
	}
	if err := ensureNamespace(ctx, cs, namespace); err != nil {
		return nil, err
	}
	if err := ensureServiceAccount(ctx, cs, namespace, saName); err != nil {
		return nil, err
	}
	if err := ensureClusterRole(ctx, cs); err != nil {
		return nil, err
	}
	if err := ensureClusterRoleBinding(ctx, cs, namespace, saName); err != nil {
		return nil, err
	}
	token, ca, err := ensureServiceAccountToken(ctx, cs, namespace, saName)
	if err != nil {
		return nil, err
	}
	if len(ca) == 0 {
		ca = restCfg.CAData
	}
	return buildServiceKubeconfig(restCfg.Host, ca, token)
}

func meninaPolicyRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: []string{"get", "list", "create"}},
		{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get", "list"}},
		{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
		{APIGroups: []string{""}, Resources: []string{"configmaps", "secrets", "persistentvolumeclaims"}, Verbs: []string{"get", "list", "watch", "create", "update"}},
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{""}, Resources: []string{"pods/log"}, Verbs: []string{"get"}},
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
	}
}

func ensureNamespace(ctx context.Context, cs kubernetes.Interface, name string) error {
	if _, err := cs.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{labelManagedBy: managedByValue}},
	}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func ensureServiceAccount(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	if _, err := cs.CoreV1().ServiceAccounts(namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	_, err := cs.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{labelManagedBy: managedByValue}},
	}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func ensureClusterRole(ctx context.Context, cs kubernetes.Interface) error {
	desired := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: PlatformClusterRoleName, Labels: map[string]string{labelManagedBy: managedByValue}},
		Rules:      meninaPolicyRules(),
	}
	existing, err := cs.RbacV1().ClusterRoles().Get(ctx, PlatformClusterRoleName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = cs.RbacV1().ClusterRoles().Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.Rules = desired.Rules
	existing.Labels = desired.Labels
	_, err = cs.RbacV1().ClusterRoles().Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func ensureClusterRoleBinding(ctx context.Context, cs kubernetes.Interface, namespace, saName string) error {
	desired := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: PlatformClusterRoleName, Labels: map[string]string{labelManagedBy: managedByValue}},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: PlatformClusterRoleName},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: saName, Namespace: namespace}},
	}
	existing, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, PlatformClusterRoleName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = cs.RbacV1().ClusterRoleBindings().Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.RoleRef = desired.RoleRef
	existing.Subjects = desired.Subjects
	existing.Labels = desired.Labels
	_, err = cs.RbacV1().ClusterRoleBindings().Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func ensureServiceAccountToken(ctx context.Context, cs kubernetes.Interface, namespace, saName string) (token, ca []byte, err error) {
	secretName := saName + "-token"
	deadline := time.Now().Add(accessTokenPollTimeout)
	for {
		secret, getErr := cs.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			_, createErr := cs.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
					Labels:    map[string]string{labelManagedBy: managedByValue},
					Annotations: map[string]string{
						corev1.ServiceAccountNameKey: saName,
					},
				},
				Type: corev1.SecretTypeServiceAccountToken,
			}, metav1.CreateOptions{})
			if createErr != nil && !apierrors.IsAlreadyExists(createErr) {
				return nil, nil, fmt.Errorf("create service account token secret: %w", createErr)
			}
		} else if getErr != nil {
			return nil, nil, getErr
		} else if len(secret.Data[corev1.ServiceAccountTokenKey]) > 0 {
			return secret.Data[corev1.ServiceAccountTokenKey], secret.Data[corev1.ServiceAccountRootCAKey], nil
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

func buildServiceKubeconfig(server string, ca, token []byte) ([]byte, error) {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[platformContextName] = &clientcmdapi.Cluster{Server: server, CertificateAuthorityData: ca}
	cfg.AuthInfos[platformContextName] = &clientcmdapi.AuthInfo{Token: string(token)}
	cfg.Contexts[platformContextName] = &clientcmdapi.Context{Cluster: platformContextName, AuthInfo: platformContextName}
	cfg.CurrentContext = platformContextName
	return clientcmd.Write(*cfg)
}
