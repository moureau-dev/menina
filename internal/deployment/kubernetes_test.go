package deployment

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newFakeCluster(t *testing.T) (*KubernetesCluster, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset()
	return NewKubernetesClusterFromInterface(cs), cs
}

func TestEnsureNamespaceIsIdempotent(t *testing.T) {
	c, _ := newFakeCluster(t)
	ctx := context.Background()
	if err := c.EnsureNamespace(ctx, "menina-apps"); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureNamespace(ctx, "menina-apps"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
}

func TestApplyWorkloadCreatesDeployment(t *testing.T) {
	c, cs := newFakeCluster(t)
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
	d, err := cs.AppsV1().Deployments("menina-apps").Get(ctx, "api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *d.Spec.Replicas != 2 || d.Spec.Template.Spec.Containers[0].Image != w.Image {
		t.Fatalf("deployment = %+v", d.Spec)
	}
	if len(d.Spec.Template.Spec.Containers[0].Env) != 1 || d.Spec.Template.Spec.Containers[0].Env[0].Name != "FOO" {
		t.Fatalf("env = %+v", d.Spec.Template.Spec.Containers[0].Env)
	}
	if err := c.ApplyWorkload(ctx, w); err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
}

func TestWorkloadStatusReportsRollout(t *testing.T) {
	c, cs := newFakeCluster(t)
	ctx := context.Background()
	w := Workload{App: "api", Name: "api", Namespace: "menina-apps", Image: "img:1", Port: 80, Replicas: 1, Type: "service"}
	_ = c.EnsureNamespace(ctx, w.Namespace)
	_ = c.ApplyWorkload(ctx, w)
	d, _ := cs.AppsV1().Deployments(w.Namespace).Get(ctx, "api", metav1.GetOptions{})
	d.Status.Replicas = 1
	d.Status.ReadyReplicas = 1
	d.Status.UpdatedReplicas = 1
	if _, err := cs.AppsV1().Deployments(w.Namespace).UpdateStatus(ctx, d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := c.WorkloadStatus(ctx, w.Namespace, "api")
	if err != nil {
		t.Fatal(err)
	}
	if st.ReadyReplicas != 1 || st.Replicas != 1 {
		t.Fatalf("status = %+v", st)
	}
	if st.Image != "img:1" {
		t.Fatalf("image = %q", st.Image)
	}
}

func TestApplyServiceAndListManaged(t *testing.T) {
	c, _ := newFakeCluster(t)
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
	c, cs := newFakeCluster(t)
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
	if _, err := cs.CoreV1().ConfigMaps("menina-registry").Get(ctx, "zot-config", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().Secrets("menina-registry").Get(ctx, "zot-htpasswd", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims("menina-registry").Get(ctx, "zot-data", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildDeploymentMountsStorageConfigAndSecret(t *testing.T) {
	c, _ := newFakeCluster(t)
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
	c, _ := newFakeCluster(t)
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
