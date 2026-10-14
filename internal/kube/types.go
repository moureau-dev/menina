package kube

// Minimal Kubernetes object types: only the fields Menina sets or reads, with
// explicit apiVersion/kind (required on writes). Quantities (cpu/memory/storage)
// are passed through as strings and validated by the API server.

type TypeMeta struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

type ObjectMeta struct {
	Name            string            `json:"name,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	UID             string            `json:"uid,omitempty"`
}

type List[T any] struct {
	Items []T `json:"items"`
}

type LabelSelector struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ContainerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

type HTTPGetAction struct {
	Path   string `json:"path,omitempty"`
	Port   int    `json:"port"`
	Scheme string `json:"scheme,omitempty"`
}

type Probe struct {
	HTTPGet *HTTPGetAction `json:"httpGet,omitempty"`
}

type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
	SubPath   string `json:"subPath,omitempty"`
}

type PVCVolumeSource struct {
	ClaimName string `json:"claimName"`
}

type ConfigMapVolumeSource struct {
	Name string `json:"name"`
}

type SecretVolumeSource struct {
	SecretName string `json:"secretName"`
}

type Volume struct {
	Name                  string                 `json:"name"`
	PersistentVolumeClaim *PVCVolumeSource       `json:"persistentVolumeClaim,omitempty"`
	ConfigMap             *ConfigMapVolumeSource `json:"configMap,omitempty"`
	Secret                *SecretVolumeSource    `json:"secret,omitempty"`
}

type LocalObjectReference struct {
	Name string `json:"name"`
}

type PodSecurityContext struct {
	FSGroup *int64 `json:"fsGroup,omitempty"`
}

type ResourceRequirements struct {
	Limits   map[string]string `json:"limits,omitempty"`
	Requests map[string]string `json:"requests,omitempty"`
}

type Container struct {
	Name            string                `json:"name"`
	Image           string                `json:"image"`
	Command         []string              `json:"command,omitempty"`
	Args            []string              `json:"args,omitempty"`
	Ports           []ContainerPort       `json:"ports,omitempty"`
	ImagePullPolicy string                `json:"imagePullPolicy,omitempty"`
	Env             []EnvVar              `json:"env,omitempty"`
	Resources       *ResourceRequirements `json:"resources,omitempty"`
	ReadinessProbe  *Probe                `json:"readinessProbe,omitempty"`
	LivenessProbe   *Probe                `json:"livenessProbe,omitempty"`
	VolumeMounts    []VolumeMount         `json:"volumeMounts,omitempty"`
}

type PodSpec struct {
	Containers       []Container            `json:"containers"`
	Volumes          []Volume               `json:"volumes,omitempty"`
	ImagePullSecrets []LocalObjectReference `json:"imagePullSecrets,omitempty"`
	SecurityContext  *PodSecurityContext    `json:"securityContext,omitempty"`
}

type PodTemplateSpec struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     PodSpec    `json:"spec"`
}

type DeploymentSpec struct {
	Replicas *int32          `json:"replicas,omitempty"`
	Selector *LabelSelector  `json:"selector,omitempty"`
	Template PodTemplateSpec `json:"template"`
}

type Condition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

type DeploymentStatus struct {
	Replicas          int32       `json:"replicas,omitempty"`
	ReadyReplicas     int32       `json:"readyReplicas,omitempty"`
	AvailableReplicas int32       `json:"availableReplicas,omitempty"`
	UpdatedReplicas   int32       `json:"updatedReplicas,omitempty"`
	Conditions        []Condition `json:"conditions,omitempty"`
}

type Deployment struct {
	TypeMeta
	Metadata ObjectMeta       `json:"metadata"`
	Spec     DeploymentSpec   `json:"spec"`
	Status   DeploymentStatus `json:"status,omitempty"`
}

type ServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int    `json:"port"`
	TargetPort int    `json:"targetPort"`
	Protocol   string `json:"protocol,omitempty"`
}

type ServiceSpec struct {
	Type      string            `json:"type,omitempty"`
	Selector  map[string]string `json:"selector,omitempty"`
	Ports     []ServicePort     `json:"ports,omitempty"`
	ClusterIP string            `json:"clusterIP,omitempty"`
}

type Service struct {
	TypeMeta
	Metadata ObjectMeta  `json:"metadata"`
	Spec     ServiceSpec `json:"spec"`
}

type Namespace struct {
	TypeMeta
	Metadata ObjectMeta `json:"metadata"`
}

type ServiceAccount struct {
	TypeMeta
	Metadata ObjectMeta `json:"metadata"`
}

type ConfigMap struct {
	TypeMeta
	Metadata ObjectMeta        `json:"metadata"`
	Data     map[string]string `json:"data,omitempty"`
}

type Secret struct {
	TypeMeta
	Metadata ObjectMeta        `json:"metadata"`
	Type     string            `json:"type,omitempty"`
	Data     map[string][]byte `json:"data,omitempty"`
}

type PVCSpec struct {
	AccessModes      []string             `json:"accessModes,omitempty"`
	StorageClassName *string              `json:"storageClassName,omitempty"`
	Resources        ResourceRequirements `json:"resources"`
}

type PersistentVolumeClaim struct {
	TypeMeta
	Metadata ObjectMeta `json:"metadata"`
	Spec     PVCSpec    `json:"spec"`
}

type PolicyRule struct {
	APIGroups []string `json:"apiGroups,omitempty"`
	Resources []string `json:"resources,omitempty"`
	Verbs     []string `json:"verbs"`
}

type ClusterRole struct {
	TypeMeta
	Metadata ObjectMeta   `json:"metadata"`
	Rules    []PolicyRule `json:"rules"`
}

type RoleRef struct {
	APIGroup string `json:"apiGroup"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

type Subject struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type ClusterRoleBinding struct {
	TypeMeta
	Metadata ObjectMeta `json:"metadata"`
	RoleRef  RoleRef    `json:"roleRef"`
	Subjects []Subject  `json:"subjects"`
}

type Pod struct {
	TypeMeta
	Metadata ObjectMeta `json:"metadata"`
}

type NodeAddress struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

type NodeStatus struct {
	Addresses []NodeAddress `json:"addresses,omitempty"`
}

type Node struct {
	TypeMeta
	Metadata ObjectMeta `json:"metadata"`
	Status   NodeStatus `json:"status,omitempty"`
}

type SelfSubjectAccessReviewSpec struct {
	ResourceAttributes *ResourceAttributes `json:"resourceAttributes,omitempty"`
}

type ResourceAttributes struct {
	Verb      string `json:"verb"`
	Group     string `json:"group,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type SelfSubjectAccessReview struct {
	TypeMeta
	Spec   SelfSubjectAccessReviewSpec `json:"spec"`
	Status struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason,omitempty"`
	} `json:"status"`
}

type CrossVersionObjectReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}

type HPAMetricTarget struct {
	Type               string `json:"type"`
	AverageUtilization *int32 `json:"averageUtilization,omitempty"`
}

type HPAResourceMetricSource struct {
	Name   string          `json:"name"`
	Target HPAMetricTarget `json:"target"`
}

type HPAMetric struct {
	Type     string                   `json:"type"`
	Resource *HPAResourceMetricSource `json:"resource,omitempty"`
}

type HPASpec struct {
	ScaleTargetRef CrossVersionObjectReference `json:"scaleTargetRef"`
	MinReplicas    *int32                      `json:"minReplicas,omitempty"`
	MaxReplicas    int32                       `json:"maxReplicas"`
	Metrics        []HPAMetric                 `json:"metrics,omitempty"`
}

type HorizontalPodAutoscaler struct {
	TypeMeta
	Metadata ObjectMeta `json:"metadata"`
	Spec     HPASpec    `json:"spec"`
}
