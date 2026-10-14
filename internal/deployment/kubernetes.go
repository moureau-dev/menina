package deployment

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	labelManagedBy = "app.kubernetes.io/managed-by"
	labelName      = "app.kubernetes.io/name"
	labelProject   = "menina.io/project"
	labelEnv       = "menina.io/environment"
	labelApp       = "menina.io/app"
	managedByValue = "menina"
)

type KubernetesCluster struct {
	client kubernetes.Interface
}

func NewKubernetesCluster(kubeconfig string) (*KubernetesCluster, error) {
	restCfg, err := buildRestConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}
	return &KubernetesCluster{client: cs}, nil
}

func NewKubernetesClusterFromInterface(cs kubernetes.Interface) *KubernetesCluster {
	return &KubernetesCluster{client: cs}
}

func NodeInternalIP(ctx context.Context, kubeconfig string) (string, error) {
	restCfg, err := buildRestConfig(kubeconfig)
	if err != nil {
		return "", err
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return "", err
	}
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				return addr.Address, nil
			}
		}
	}
	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeExternalIP {
				return addr.Address, nil
			}
		}
	}
	return "", fmt.Errorf("no node internal address found")
}

func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	candidates := []string{kubeconfig, os.Getenv("KUBECONFIG"), "/etc/rancher/k3s/k3s.yaml", "/etc/rancher/rke2/rke2.yaml"}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return clientcmd.BuildConfigFromFlags("", candidate)
		}
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
}

func (k *KubernetesCluster) Ping(ctx context.Context) error {
	_, err := k.client.Discovery().ServerVersion()
	return err
}

func (k *KubernetesCluster) EnsureNamespace(ctx context.Context, name string) error {
	_, err := k.client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = k.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{labelManagedBy: managedByValue}},
	}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) ApplyWorkload(ctx context.Context, w Workload) error {
	desired := k.buildDeployment(w)
	existing, err := k.client.AppsV1().Deployments(w.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.AppsV1().Deployments(w.Namespace).Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	_, err = k.client.AppsV1().Deployments(w.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func (k *KubernetesCluster) DeleteWorkload(ctx context.Context, namespace, name string) error {
	err := k.client.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) DeleteService(ctx context.Context, namespace, name string) error {
	err := k.client.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) RestartWorkload(ctx context.Context, namespace, name string) error {
	d, err := k.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if d.Spec.Template.Annotations == nil {
		d.Spec.Template.Annotations = map[string]string{}
	}
	d.Spec.Template.Annotations["menina.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	_, err = k.client.AppsV1().Deployments(namespace).Update(ctx, d, metav1.UpdateOptions{})
	return err
}

func (k *KubernetesCluster) ListWorkloads(ctx context.Context) ([]WorkloadRef, error) {
	list, err := k.client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{
		LabelSelector: labelManagedBy + "=" + managedByValue,
	})
	if err != nil {
		return nil, err
	}
	var out []WorkloadRef
	for _, d := range list.Items {
		out = append(out, WorkloadRef{Namespace: d.Namespace, Name: d.Name})
	}
	return out, nil
}

func (k *KubernetesCluster) ApplyService(ctx context.Context, w Workload) error {
	name := w.ServiceName
	if name == "" {
		name = w.Name
	}
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: w.Namespace,
			Labels:    k.labels(w),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: k.selector(w),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       int32(w.Port),
				TargetPort: intstr.FromInt32(int32(w.Port)),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	existing, err := k.client.CoreV1().Services(w.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.CoreV1().Services(w.Namespace).Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	existing.Spec.Selector = desired.Spec.Selector
	existing.Spec.Ports = desired.Spec.Ports
	existing.Labels = desired.Labels
	_, err = k.client.CoreV1().Services(w.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func (k *KubernetesCluster) WorkloadStatus(ctx context.Context, namespace, name string) (*WorkloadStatus, error) {
	d, err := k.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	st := &WorkloadStatus{
		Replicas:          int(d.Status.Replicas),
		ReadyReplicas:     int(d.Status.ReadyReplicas),
		AvailableReplicas: int(d.Status.AvailableReplicas),
		UpdatedReplicas:   int(d.Status.UpdatedReplicas),
	}
	if len(d.Spec.Template.Spec.Containers) > 0 {
		st.Image = d.Spec.Template.Spec.Containers[0].Image
	}
	for _, c := range d.Status.Conditions {
		st.Conditions = append(st.Conditions, fmt.Sprintf("%s=%s", c.Type, c.Status))
	}
	return st, nil
}

func (k *KubernetesCluster) ServiceClusterIP(ctx context.Context, namespace, name string) (string, error) {
	svc, err := k.client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return svc.Spec.ClusterIP, nil
}

func (k *KubernetesCluster) ListServices(ctx context.Context) ([]ServiceRef, error) {
	list, err := k.client.CoreV1().Services("").List(ctx, metav1.ListOptions{
		LabelSelector: labelManagedBy + "=" + managedByValue,
	})
	if err != nil {
		return nil, err
	}
	var out []ServiceRef
	for _, svc := range list.Items {
		ref := ServiceRef{Namespace: svc.Namespace, Name: svc.Name, ClusterIP: svc.Spec.ClusterIP, Managed: true}
		for _, p := range svc.Spec.Ports {
			ref.Ports = append(ref.Ports, int(p.Port))
		}
		out = append(out, ref)
	}
	return out, nil
}

func (k *KubernetesCluster) PodLogs(ctx context.Context, namespace, name string, opts LogOptions) (io.ReadCloser, error) {
	pods, err := k.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelName + "=" + name})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no pods found for %s/%s", namespace, name)
	}
	pod := pods.Items[0]
	logOpts := &corev1.PodLogOptions{Follow: opts.Follow}
	if opts.Container != "" {
		logOpts.Container = opts.Container
	}
	if opts.TailLines != nil {
		logOpts.TailLines = opts.TailLines
	}
	return k.client.CoreV1().Pods(namespace).GetLogs(pod.Name, logOpts).Stream(ctx)
}

func (k *KubernetesCluster) EnsureSecret(ctx context.Context, namespace, name string, data map[string][]byte) error {
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{labelManagedBy: managedByValue}},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}
	existing, err := k.client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.CoreV1().Secrets(namespace).Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.Data = data
	_, err = k.client.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func (k *KubernetesCluster) EnsurePVC(ctx context.Context, namespace, name, size, storageClass string) error {
	_, err := k.client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	q, err := resource.ParseQuantity(size)
	if err != nil {
		return fmt.Errorf("parse storage size %q: %w", size, err)
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{labelManagedBy: managedByValue}},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: q},
			},
		},
	}
	if storageClass != "" {
		pvc.Spec.StorageClassName = &storageClass
	}
	_, err = k.client.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) EnsureConfigMap(ctx context.Context, namespace, name string, data map[string]string) error {
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{labelManagedBy: managedByValue}},
		Data:       data,
	}
	existing, err := k.client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.CoreV1().ConfigMaps(namespace).Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.Data = data
	_, err = k.client.CoreV1().ConfigMaps(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func (k *KubernetesCluster) buildDeployment(w Workload) *appsv1.Deployment {
	replicas := int32(w.Replicas)
	if w.Type == "worker" && replicas == 0 {
		replicas = 1
	}
	container := corev1.Container{
		Name:    w.Name,
		Image:   w.Image,
		Command: w.Command,
		Args:    w.Args,
	}
	containerPort := w.Port
	if w.ContainerPort > 0 {
		containerPort = w.ContainerPort
	}
	if containerPort > 0 {
		container.Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: int32(containerPort), Protocol: corev1.ProtocolTCP}}
	}
	if w.PullPolicy != "" {
		container.ImagePullPolicy = corev1.PullPolicy(w.PullPolicy)
	} else {
		container.ImagePullPolicy = corev1.PullIfNotPresent
	}
	keys := make([]string, 0, len(w.Env))
	for key := range w.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		container.Env = append(container.Env, corev1.EnvVar{Name: key, Value: w.Env[key]})
	}
	if w.CPU != "" || w.Memory != "" {
		req := corev1.ResourceList{}
		lim := corev1.ResourceList{}
		if w.CPU != "" {
			if q, err := resource.ParseQuantity(w.CPU); err == nil {
				req[corev1.ResourceCPU] = q
				lim[corev1.ResourceCPU] = q
			}
		}
		if w.Memory != "" {
			if q, err := resource.ParseQuantity(w.Memory); err == nil {
				req[corev1.ResourceMemory] = q
				lim[corev1.ResourceMemory] = q
			}
		}
		container.Resources = corev1.ResourceRequirements{Requests: req, Limits: lim}
	}
	if w.HealthPath != "" {
		probe := &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: w.HealthPath, Port: intstr.FromInt32(int32(w.Port))},
			},
		}
		container.ReadinessProbe = probe
		container.LivenessProbe = probe.DeepCopy()
	}
	podSpec := corev1.PodSpec{Containers: []corev1.Container{container}}
	if w.PullSecret != "" {
		podSpec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: w.PullSecret}}
	}
	if w.StorageSize != "" && w.MountPath != "" {
		fsGroup := int64(1000)
		podSpec.SecurityContext = &corev1.PodSecurityContext{FSGroup: &fsGroup}
		volumeName := "data"
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: w.Name + "-data"},
			},
		})
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: volumeName, MountPath: w.MountPath})
	}
	if w.ConfigMap != "" {
		mount := w.ConfigMount
		if mount == "" {
			mount = "/etc/" + w.Name
		}
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: w.ConfigMap}},
			},
		})
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "config", MountPath: mount, ReadOnly: true})
	}
	if w.SecretName != "" && w.SecretMount != "" {
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: "secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: w.SecretName},
			},
		})
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "secret", MountPath: w.SecretMount, ReadOnly: true})
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: w.Name, Namespace: w.Namespace, Labels: k.labels(w)},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: k.selector(w)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: k.selector(w)},
				Spec:       podSpec,
			},
		},
	}
}

func (k *KubernetesCluster) labels(w Workload) map[string]string {
	l := k.selector(w)
	l[labelManagedBy] = managedByValue
	return l
}

func (k *KubernetesCluster) selector(w Workload) map[string]string {
	return map[string]string{labelName: w.Name}
}

func ParseMemoryToBytes(q string) (int64, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return 0, nil
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(q, "Gi"):
		mult = 1024 * 1024 * 1024
		q = strings.TrimSuffix(q, "Gi")
	case strings.HasSuffix(q, "Mi"):
		mult = 1024 * 1024
		q = strings.TrimSuffix(q, "Mi")
	case strings.HasSuffix(q, "Ki"):
		mult = 1024
		q = strings.TrimSuffix(q, "Ki")
	case strings.HasSuffix(q, "G"):
		mult = 1000 * 1000 * 1000
		q = strings.TrimSuffix(q, "G")
	case strings.HasSuffix(q, "M"):
		mult = 1000 * 1000
		q = strings.TrimSuffix(q, "M")
	case strings.HasSuffix(q, "K"):
		mult = 1000
		q = strings.TrimSuffix(q, "K")
	}
	n, err := strconv.ParseFloat(q, 64)
	if err != nil {
		return 0, err
	}
	return int64(n * float64(mult)), nil
}
