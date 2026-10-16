package deployment

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moureau-dev/menina/internal/kube"
)

const (
	labelManagedBy = "app.kubernetes.io/managed-by"
	labelName      = "app.kubernetes.io/name"
	labelProject   = "menina.io/project"
	labelEnv       = "menina.io/environment"
	labelApp       = "menina.io/app"
	managedByValue = "menina"

	coreV1        = "/api/v1"
	appsV1        = "/apis/apps/v1"
	rbacV1        = "/apis/rbac.authorization.k8s.io/v1"
	autoscalingV2 = "/apis/autoscaling/v2"
)

type KubernetesCluster struct {
	client *kube.Client
}

func NewKubernetesCluster(kubeconfig string) (*KubernetesCluster, error) {
	cfg, err := kube.Load(kubeconfig)
	if err != nil {
		return nil, err
	}
	client, err := kube.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}
	return &KubernetesCluster{client: client}, nil
}

func NewKubernetesClusterFromClient(client *kube.Client) *KubernetesCluster {
	return &KubernetesCluster{client: client}
}

func NodeInternalIP(ctx context.Context, kubeconfig string) (string, error) {
	cluster, err := NewKubernetesCluster(kubeconfig)
	if err != nil {
		return "", err
	}
	nodes, err := cluster.listNodes(ctx)
	if err != nil {
		return "", err
	}
	for _, node := range nodes {
		for _, addr := range node.Status.Addresses {
			if addr.Type == "InternalIP" {
				return addr.Address, nil
			}
		}
	}
	for _, node := range nodes {
		for _, addr := range node.Status.Addresses {
			if addr.Type == "ExternalIP" {
				return addr.Address, nil
			}
		}
	}
	return "", fmt.Errorf("no node internal address found")
}

func (k *KubernetesCluster) listNodes(ctx context.Context) ([]kube.Node, error) {
	var list kube.List[kube.Node]
	if err := k.client.List(ctx, coreV1+"/nodes", "", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (k *KubernetesCluster) Ping(ctx context.Context) error {
	_, err := k.client.Version(ctx)
	return err
}

func nsResource(prefix, namespace, resource string) string {
	if namespace == "" {
		return prefix + "/" + resource
	}
	return prefix + "/namespaces/" + namespace + "/" + resource
}

func (k *KubernetesCluster) EnsureNamespace(ctx context.Context, name string) error {
	if err := k.client.Get(ctx, coreV1+"/namespaces/"+name, &kube.Namespace{}); err == nil {
		return nil
	} else if !kube.IsNotFound(err) {
		return err
	}
	err := k.client.Create(ctx, coreV1+"/namespaces", &kube.Namespace{
		TypeMeta: kube.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		Metadata: kube.ObjectMeta{Name: name, Labels: map[string]string{labelManagedBy: managedByValue}},
	}, nil)
	if kube.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) ApplyWorkload(ctx context.Context, w Workload) error {
	collection := nsResource(appsV1, w.Namespace, "deployments")
	item := collection + "/" + w.Name
	desired := k.buildDeployment(w)

	existing := map[string]any{}
	err := k.client.Get(ctx, item, &existing)
	if kube.IsNotFound(err) {
		if err := k.client.Create(ctx, collection, desired, nil); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		desiredMap := kube.ToMap(desired)
		existing["spec"] = desiredMap["spec"]
		meta, _ := existing["metadata"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
			existing["metadata"] = meta
		}
		meta["labels"] = k.labels(w)
		if err := k.client.Update(ctx, item, existing, nil); err != nil {
			return err
		}
	}
	return k.applyAutoscaler(ctx, w)
}

func (k *KubernetesCluster) applyAutoscaler(ctx context.Context, w Workload) error {
	if w.MaxReplicas > w.Replicas {
		return k.ensureAutoscaler(ctx, w)
	}
	// No autoscaling: remove any stale HPA.
	return k.DeleteAutoscaler(ctx, w.Namespace, w.Name)
}

func (k *KubernetesCluster) ensureAutoscaler(ctx context.Context, w Workload) error {
	collection := nsResource(autoscalingV2, w.Namespace, "horizontalpodautoscalers")
	item := collection + "/" + w.Name
	min := int32(w.Replicas)
	cpu := int32(w.TargetCPU)
	desired := &kube.HorizontalPodAutoscaler{
		TypeMeta: kube.TypeMeta{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscaler"},
		Metadata: kube.ObjectMeta{Name: w.Name, Namespace: w.Namespace, Labels: k.labels(w)},
		Spec: kube.HPASpec{
			ScaleTargetRef: kube.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: w.Name},
			MinReplicas:    &min,
			MaxReplicas:    int32(w.MaxReplicas),
			Metrics: []kube.HPAMetric{{
				Type:     "Resource",
				Resource: &kube.HPAResourceMetricSource{Name: "cpu", Target: kube.HPAMetricTarget{Type: "Utilization", AverageUtilization: &cpu}},
			}},
		},
	}
	var existing kube.HorizontalPodAutoscaler
	err := k.client.Get(ctx, item, &existing)
	if kube.IsNotFound(err) {
		return k.client.Create(ctx, collection, desired, nil)
	}
	if err != nil {
		return err
	}
	existing.Spec = desired.Spec
	existing.Metadata.Labels = desired.Metadata.Labels
	return k.client.Update(ctx, item, &existing, nil)
}

func (k *KubernetesCluster) DeleteAutoscaler(ctx context.Context, namespace, name string) error {
	err := k.client.Delete(ctx, nsResource(autoscalingV2, namespace, "horizontalpodautoscalers")+"/"+name)
	if kube.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) DeleteWorkload(ctx context.Context, namespace, name string) error {
	err := k.client.Delete(ctx, nsResource(appsV1, namespace, "deployments")+"/"+name)
	if kube.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) DeleteService(ctx context.Context, namespace, name string) error {
	err := k.client.Delete(ctx, nsResource(coreV1, namespace, "services")+"/"+name)
	if kube.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) RestartWorkload(ctx context.Context, namespace, name string) error {
	item := nsResource(appsV1, namespace, "deployments") + "/" + name
	existing := map[string]any{}
	if err := k.client.Get(ctx, item, &existing); err != nil {
		return err
	}
	spec, _ := existing["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
		existing["spec"] = spec
	}
	template, _ := spec["template"].(map[string]any)
	if template == nil {
		template = map[string]any{}
		spec["template"] = template
	}
	meta, _ := template["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		template["metadata"] = meta
	}
	ann, _ := meta["annotations"].(map[string]any)
	if ann == nil {
		ann = map[string]any{}
		meta["annotations"] = ann
	}
	ann["menina.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	return k.client.Update(ctx, item, existing, nil)
}

func (k *KubernetesCluster) ScaleWorkload(ctx context.Context, namespace, name string, replicas int) error {
	item := nsResource(appsV1, namespace, "deployments") + "/" + name
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		existing := map[string]any{}
		if err := k.client.Get(ctx, item, &existing); err != nil {
			return err
		}
		spec, _ := existing["spec"].(map[string]any)
		if spec == nil {
			spec = map[string]any{}
			existing["spec"] = spec
		}
		spec["replicas"] = replicas
		err := k.client.Update(ctx, item, existing, nil)
		if err == nil {
			return nil
		}
		if kube.IsConflict(err) {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		return err
	}
	return lastErr
}

func (k *KubernetesCluster) ListWorkloads(ctx context.Context) ([]WorkloadRef, error) {
	var list kube.List[kube.Deployment]
	if err := k.client.List(ctx, appsV1+"/deployments", labelManagedBy+"="+managedByValue, &list); err != nil {
		return nil, err
	}
	var out []WorkloadRef
	for _, d := range list.Items {
		ref := WorkloadRef{Namespace: d.Metadata.Namespace, Name: d.Metadata.Name}
		if d.Metadata.Labels != nil {
			ref.Project = d.Metadata.Labels[labelProject]
			ref.Environment = d.Metadata.Labels[labelEnv]
			ref.App = d.Metadata.Labels[labelApp]
		}
		out = append(out, ref)
	}
	return out, nil
}

func (k *KubernetesCluster) ApplyService(ctx context.Context, w Workload) error {
	name := w.ServiceName
	if name == "" {
		name = w.Name
	}
	collection := nsResource(coreV1, w.Namespace, "services")
	item := collection + "/" + name
	desired := &kube.Service{
		TypeMeta: kube.TypeMeta{APIVersion: "v1", Kind: "Service"},
		Metadata: kube.ObjectMeta{Name: name, Namespace: w.Namespace, Labels: k.labels(w)},
		Spec: kube.ServiceSpec{
			Type:     "ClusterIP",
			Selector: k.selector(w),
			Ports:    []kube.ServicePort{{Name: "http", Port: w.Port, TargetPort: w.Port, Protocol: "TCP"}},
		},
	}
	existing := map[string]any{}
	err := k.client.Get(ctx, item, &existing)
	if kube.IsNotFound(err) {
		return k.client.Create(ctx, collection, desired, nil)
	}
	if err != nil {
		return err
	}
	desiredMap := kube.ToMap(desired)
	desiredSpec, _ := desiredMap["spec"].(map[string]any)
	spec, _ := existing["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
		existing["spec"] = spec
	}
	// Preserve server-assigned/immutable fields (clusterIP, ipFamilies, ...):
	// only replace selector and ports.
	spec["selector"] = desiredSpec["selector"]
	spec["ports"] = desiredSpec["ports"]
	if _, ok := spec["type"]; !ok {
		spec["type"] = "ClusterIP"
	}
	meta, _ := existing["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		existing["metadata"] = meta
	}
	meta["labels"] = k.labels(w)
	return k.client.Update(ctx, item, existing, nil)
}

func (k *KubernetesCluster) WorkloadStatus(ctx context.Context, namespace, name string) (*WorkloadStatus, error) {
	var d kube.Deployment
	if err := k.client.Get(ctx, nsResource(appsV1, namespace, "deployments")+"/"+name, &d); err != nil {
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
	var svc kube.Service
	if err := k.client.Get(ctx, nsResource(coreV1, namespace, "services")+"/"+name, &svc); err != nil {
		return "", err
	}
	return svc.Spec.ClusterIP, nil
}

func (k *KubernetesCluster) ListServices(ctx context.Context) ([]ServiceRef, error) {
	var list kube.List[kube.Service]
	if err := k.client.List(ctx, coreV1+"/services", labelManagedBy+"="+managedByValue, &list); err != nil {
		return nil, err
	}
	var out []ServiceRef
	for _, svc := range list.Items {
		ref := ServiceRef{Namespace: svc.Metadata.Namespace, Name: svc.Metadata.Name, ClusterIP: svc.Spec.ClusterIP, Managed: true}
		for _, p := range svc.Spec.Ports {
			ref.Ports = append(ref.Ports, p.Port)
		}
		out = append(out, ref)
	}
	return out, nil
}

func (k *KubernetesCluster) PodLogs(ctx context.Context, namespace, name string, opts LogOptions) (io.ReadCloser, error) {
	var pods kube.List[kube.Pod]
	if err := k.client.List(ctx, nsResource(coreV1, namespace, "pods"), labelName+"="+name, &pods); err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no pods found for %s/%s", namespace, name)
	}
	q := url.Values{}
	if opts.Follow {
		q.Set("follow", "true")
	}
	if opts.Container != "" {
		q.Set("container", opts.Container)
	}
	if opts.TailLines != nil {
		q.Set("tailLines", strconv.FormatInt(*opts.TailLines, 10))
	}
	return k.client.Stream(ctx, nsResource(coreV1, namespace, "pods")+"/"+pods.Items[0].Metadata.Name+"/log", q)
}

func (k *KubernetesCluster) EnsureSecret(ctx context.Context, namespace, name string, data map[string][]byte) error {
	collection := nsResource(coreV1, namespace, "secrets")
	item := collection + "/" + name
	desired := &kube.Secret{
		TypeMeta: kube.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		Metadata: kube.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{labelManagedBy: managedByValue}},
		Type:     "Opaque",
		Data:     data,
	}
	existing := map[string]any{}
	err := k.client.Get(ctx, item, &existing)
	if kube.IsNotFound(err) {
		return k.client.Create(ctx, collection, desired, nil)
	}
	if err != nil {
		return err
	}
	existing["data"] = kube.ToMap(desired)["data"]
	return k.client.Update(ctx, item, existing, nil)
}

func (k *KubernetesCluster) EnsureConfigMap(ctx context.Context, namespace, name string, data map[string]string) error {
	collection := nsResource(coreV1, namespace, "configmaps")
	item := collection + "/" + name
	desired := &kube.ConfigMap{
		TypeMeta: kube.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		Metadata: kube.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{labelManagedBy: managedByValue}},
		Data:     data,
	}
	existing := map[string]any{}
	err := k.client.Get(ctx, item, &existing)
	if kube.IsNotFound(err) {
		return k.client.Create(ctx, collection, desired, nil)
	}
	if err != nil {
		return err
	}
	existing["data"] = kube.ToMap(desired)["data"]
	return k.client.Update(ctx, item, existing, nil)
}

func (k *KubernetesCluster) EnsurePVC(ctx context.Context, namespace, name, size, storageClass string) error {
	collection := nsResource(coreV1, namespace, "persistentvolumeclaims")
	item := collection + "/" + name
	if err := k.client.Get(ctx, item, &kube.PersistentVolumeClaim{}); err == nil {
		return nil
	} else if !kube.IsNotFound(err) {
		return err
	}
	if size == "" {
		size = "1Gi"
	}
	pvc := &kube.PersistentVolumeClaim{
		TypeMeta: kube.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		Metadata: kube.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{labelManagedBy: managedByValue}},
		Spec: kube.PVCSpec{
			AccessModes: []string{"ReadWriteOnce"},
			Resources:   kube.ResourceRequirements{Requests: map[string]string{"storage": size}},
		},
	}
	if storageClass != "" {
		pvc.Spec.StorageClassName = &storageClass
	}
	err := k.client.Create(ctx, collection, pvc, nil)
	if kube.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) DeletePVC(ctx context.Context, namespace, name string) error {
	err := k.client.Delete(ctx, nsResource(coreV1, namespace, "persistentvolumeclaims")+"/"+name)
	if kube.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *KubernetesCluster) buildDeployment(w Workload) *kube.Deployment {
	replicas := int32(w.Replicas)
	if w.Type == "worker" && replicas == 0 {
		replicas = 1
	}
	container := kube.Container{
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
		container.Ports = []kube.ContainerPort{{Name: "http", ContainerPort: containerPort, Protocol: "TCP"}}
	}
	// Raw L4 exposure: bind the requested TCP/UDP ports on the node via hostPort.
	for _, p := range w.Ports {
		proto := strings.ToUpper(p.Protocol)
		if proto == "" {
			proto = "TCP"
		}
		name := fmt.Sprintf("%s-%d", strings.ToLower(proto), p.Port)
		container.Ports = append(container.Ports, kube.ContainerPort{
			Name:          name,
			ContainerPort: p.Port,
			HostPort:      p.Port,
			Protocol:      proto,
		})
	}
	if w.PullPolicy != "" {
		container.ImagePullPolicy = w.PullPolicy
	} else {
		container.ImagePullPolicy = "IfNotPresent"
	}
	keys := make([]string, 0, len(w.Env))
	for key := range w.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		container.Env = append(container.Env, kube.EnvVar{Name: key, Value: w.Env[key]})
	}
	if w.SecretEnvName != "" {
		secretKeys := make([]string, 0, len(w.SecretEnv))
		for envName := range w.SecretEnv {
			secretKeys = append(secretKeys, envName)
		}
		sort.Strings(secretKeys)
		for _, envName := range secretKeys {
			container.Env = append(container.Env, kube.EnvVar{
				Name: envName,
				ValueFrom: &kube.EnvVarSource{
					SecretKeyRef: &kube.SecretKeySelector{Name: w.SecretEnvName, Key: w.SecretEnv[envName]},
				},
			})
		}
	}
	if w.CPU != "" || w.Memory != "" {
		req := map[string]string{}
		lim := map[string]string{}
		if w.CPU != "" {
			req["cpu"] = w.CPU
			lim["cpu"] = w.CPU
		}
		if w.Memory != "" {
			req["memory"] = w.Memory
			lim["memory"] = w.Memory
		}
		container.Resources = &kube.ResourceRequirements{Requests: req, Limits: lim}
	}
	if w.HealthPath != "" {
		probe := &kube.Probe{HTTPGet: &kube.HTTPGetAction{Path: w.HealthPath, Port: w.Port}}
		container.ReadinessProbe = probe
		container.LivenessProbe = &kube.Probe{HTTPGet: &kube.HTTPGetAction{Path: w.HealthPath, Port: w.Port}}
	}
	podSpec := kube.PodSpec{Containers: []kube.Container{container}}
	if w.PullSecret != "" {
		podSpec.ImagePullSecrets = []kube.LocalObjectReference{{Name: w.PullSecret}}
	}
	if w.StorageSize != "" && w.MountPath != "" {
		fsGroup := int64(1000)
		podSpec.SecurityContext = &kube.PodSecurityContext{FSGroup: &fsGroup}
		podSpec.Volumes = append(podSpec.Volumes, kube.Volume{
			Name:                  "data",
			PersistentVolumeClaim: &kube.PVCVolumeSource{ClaimName: w.Name + "-data"},
		})
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, kube.VolumeMount{Name: "data", MountPath: w.MountPath})
	}
	if w.ConfigMap != "" {
		mount := w.ConfigMount
		if mount == "" {
			mount = "/etc/" + w.Name
		}
		podSpec.Volumes = append(podSpec.Volumes, kube.Volume{
			Name:      "config",
			ConfigMap: &kube.ConfigMapVolumeSource{Name: w.ConfigMap},
		})
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, kube.VolumeMount{Name: "config", MountPath: mount, ReadOnly: true})
	}
	if w.SecretName != "" && w.SecretMount != "" {
		podSpec.Volumes = append(podSpec.Volumes, kube.Volume{
			Name:   "secret",
			Secret: &kube.SecretVolumeSource{SecretName: w.SecretName},
		})
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, kube.VolumeMount{Name: "secret", MountPath: w.SecretMount, ReadOnly: true})
	}
	return &kube.Deployment{
		TypeMeta: kube.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		Metadata: kube.ObjectMeta{Name: w.Name, Namespace: w.Namespace, Labels: k.labels(w)},
		Spec: kube.DeploymentSpec{
			Replicas: &replicas,
			Selector: &kube.LabelSelector{MatchLabels: k.selector(w)},
			Template: kube.PodTemplateSpec{
				Metadata: kube.ObjectMeta{Labels: k.selector(w)},
				Spec:     podSpec,
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
