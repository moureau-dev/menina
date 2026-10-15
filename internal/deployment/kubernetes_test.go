package deployment

import (
	"context"
	"testing"

	"github.com/moureau-dev/menina/internal/kube"
)

func TestEnsureNamespaceIsIdempotent(t *testing.T) {
	c := newFakeAPI(t).cluster(t)
	ctx := context.Background()
	if err := c.EnsureNamespace(ctx, "menina-apps"); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureNamespace(ctx, "menina-apps"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
}

func TestApplyWorkloadCreatesDeployment(t *testing.T) {
	f := newFakeAPI(t)
	c := f.cluster(t)
	ctx := context.Background()
	w := Workload{
		Project: "p", Environment: "prod", App: "api", Name: "api", Namespace: "menina-apps",
		Image: "registry/api@sha256:abc", Port: 8080, Replicas: 2, Type: "service",
		Env: map[string]string{"FOO": "bar"}, CPU: "500m", Memory: "512Mi",
	}
	if err := c.EnsureNamespace(ctx, w.Namespace); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyWorkload(ctx, w); err != nil {
		t.Fatal(err)
	}
	var d kube.Deployment
	if err := c.client.Get(ctx, "/apis/apps/v1/namespaces/menina-apps/deployments/api", &d); err != nil {
		t.Fatal(err)
	}
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 2 || d.Spec.Template.Spec.Containers[0].Image != w.Image {
		t.Fatalf("deployment = %+v", d.Spec)
	}
	if len(d.Spec.Template.Spec.Containers[0].Env) != 1 || d.Spec.Template.Spec.Containers[0].Env[0].Name != "FOO" {
		t.Fatalf("env = %+v", d.Spec.Template.Spec.Containers[0].Env)
	}
	// Idempotent apply must preserve the object (same name/resourceVersion).
	if err := c.ApplyWorkload(ctx, w); err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
}

func TestWorkloadStatusReportsRollout(t *testing.T) {
	f := newFakeAPI(t)
	c := f.cluster(t)
	ctx := context.Background()
	w := Workload{App: "api", Name: "api", Namespace: "menina-apps", Image: "img:1", Port: 80, Replicas: 1, Type: "service"}
	_ = c.EnsureNamespace(ctx, w.Namespace)
	_ = c.ApplyWorkload(ctx, w)
	obj := f.object("/apis/apps/v1/namespaces/menina-apps/deployments", "api")
	obj["status"] = map[string]any{"replicas": 1, "readyReplicas": 1, "updatedReplicas": 1}
	st, err := c.WorkloadStatus(ctx, w.Namespace, "api")
	if err != nil {
		t.Fatal(err)
	}
	if st.ReadyReplicas != 1 || st.Replicas != 1 || st.Image != "img:1" {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyServiceAndListManaged(t *testing.T) {
	c := newFakeAPI(t).cluster(t)
	ctx := context.Background()
	w := Workload{App: "api", Name: "api", Namespace: "menina-apps", Port: 8080, Type: "service"}
	_ = c.EnsureNamespace(ctx, w.Namespace)
	if err := c.ApplyService(ctx, w); err != nil {
		t.Fatal(err)
	}
	services, err := c.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range services {
		if s.Name == "api" && s.Managed {
			found = true
		}
	}
	if !found {
		t.Fatalf("managed service not listed: %+v", services)
	}
}

func TestEnsureConfigMapSecretAndPVC(t *testing.T) {
	f := newFakeAPI(t)
	c := f.cluster(t)
	ctx := context.Background()
	_ = c.EnsureNamespace(ctx, "menina-registry")
	if err := c.EnsureConfigMap(ctx, "menina-registry", "zot-config", map[string]string{"config.json": "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureSecret(ctx, "menina-registry", "zot-htpasswd", map[string][]byte{"htpasswd": []byte("a:b")}); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsurePVC(ctx, "menina-registry", "zot-data", "10Gi", ""); err != nil {
		t.Fatal(err)
	}
	if f.object("/api/v1/namespaces/menina-registry/configmaps", "zot-config") == nil {
		t.Fatal("configmap missing")
	}
	if f.object("/api/v1/namespaces/menina-registry/secrets", "zot-htpasswd") == nil {
		t.Fatal("secret missing")
	}
	if f.object("/api/v1/namespaces/menina-registry/persistentvolumeclaims", "zot-data") == nil {
		t.Fatal("pvc missing")
	}
}

func TestBuildDeploymentMountsStorageConfigAndSecret(t *testing.T) {
	c := newFakeAPI(t).cluster(t)
	w := Workload{
		App: "zot", Name: "zot", Namespace: "ns", Image: "zot:2", Port: 5000, ContainerPort: 5000,
		StorageSize: "10Gi", MountPath: "/var/lib/registry",
		ConfigMap: "zot-config", ConfigMount: "/etc/zot",
		SecretName: "zot-htpasswd", SecretMount: "/etc/zot-auth",
	}
	d := c.buildDeployment(w)
	pod := d.Spec.Template.Spec
	if len(pod.Volumes) != 3 {
		t.Fatalf("volumes = %+v", pod.Volumes)
	}
	mounts := pod.Containers[0].VolumeMounts
	if len(mounts) != 3 {
		t.Fatalf("mounts = %+v", mounts)
	}
	if pod.Containers[0].Ports[0].ContainerPort != 5000 {
		t.Fatalf("port = %d", pod.Containers[0].Ports[0].ContainerPort)
	}
}

func TestBuildDeploymentInjectsSecretEnv(t *testing.T) {
	c := newFakeAPI(t).cluster(t)
	w := Workload{
		App: "api", Name: "api", Namespace: "ns", Image: "img:1", Port: 8080,
		Env:           map[string]string{"LOG_LEVEL": "info"},
		SecretEnvName: "api-secrets",
		SecretEnv:     map[string]string{"DATABASE_URL": "DATABASE_URL"},
	}
	d := c.buildDeployment(w)
	env := d.Spec.Template.Spec.Containers[0].Env
	var secretEnv *kube.EnvVar
	for i := range env {
		if env[i].Name == "DATABASE_URL" {
			secretEnv = &env[i]
		}
	}
	if secretEnv == nil || secretEnv.ValueFrom == nil || secretEnv.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("secret env missing or malformed: %+v", env)
	}
	ref := secretEnv.ValueFrom.SecretKeyRef
	if ref.Name != "api-secrets" || ref.Key != "DATABASE_URL" {
		t.Fatalf("secret key ref = %+v", ref)
	}
}

func TestBuildDeploymentHostPortsL4(t *testing.T) {
	c := newFakeAPI(t).cluster(t)
	w := Workload{
		App: "game", Name: "game", Namespace: "ns", Image: "game:1",
		Ports: []PortMapping{{Port: 25565, Protocol: "TCP"}, {Port: 19132, Protocol: "UDP"}},
	}
	d := c.buildDeployment(w)
	ports := d.Spec.Template.Spec.Containers[0].Ports
	byProto := map[string]kube.ContainerPort{}
	for _, p := range ports {
		byProto[p.Protocol] = p
	}
	tcp, ok := byProto["TCP"]
	if !ok || tcp.ContainerPort != 25565 || tcp.HostPort != 25565 {
		t.Fatalf("tcp port mapping = %+v", byProto)
	}
	udp, ok := byProto["UDP"]
	if !ok || udp.ContainerPort != 19132 || udp.HostPort != 19132 {
		t.Fatalf("udp port mapping = %+v", byProto)
	}
}

func TestApplyWorkloadManagesAutoscaler(t *testing.T) {
	f := newFakeAPI(t)
	c := f.cluster(t)
	ctx := context.Background()
	w := Workload{Name: "api", Namespace: "ns", Image: "i:1", Port: 80, Replicas: 2, MaxReplicas: 10, TargetCPU: 70, Type: "service"}
	_ = c.EnsureNamespace(ctx, "ns")
	if err := c.ApplyWorkload(ctx, w); err != nil {
		t.Fatal(err)
	}
	if f.object("/apis/autoscaling/v2/namespaces/ns/horizontalpodautoscalers", "api") == nil {
		t.Fatal("expected an HPA when max > min")
	}
	// Switching back to a fixed count must remove the HPA.
	w.MaxReplicas = 0
	if err := c.ApplyWorkload(ctx, w); err != nil {
		t.Fatal(err)
	}
	if f.object("/apis/autoscaling/v2/namespaces/ns/horizontalpodautoscalers", "api") != nil {
		t.Fatal("HPA should be removed when autoscaling is disabled")
	}
}

func TestParseMemoryToBytes(t *testing.T) {
	cases := map[string]int64{"512Mi": 512 * 1024 * 1024, "1Gi": 1024 * 1024 * 1024, "500M": 500 * 1000 * 1000}
	for in, want := range cases {
		got, err := ParseMemoryToBytes(in)
		if err != nil || got != want {
			t.Fatalf("ParseMemoryToBytes(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := ParseMemoryToBytes("nope"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestDeleteWorkload(t *testing.T) {
	c := newFakeAPI(t).cluster(t)
	ctx := context.Background()
	w := Workload{App: "api", Name: "api", Namespace: "ns", Image: "i:1", Port: 80, Replicas: 1, Type: "service"}
	_ = c.EnsureNamespace(ctx, "ns")
	_ = c.ApplyWorkload(ctx, w)
	if err := c.DeleteWorkload(ctx, "ns", "api"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteWorkload(ctx, "ns", "api"); err != nil {
		t.Fatalf("delete should be idempotent: %v", err)
	}
}
