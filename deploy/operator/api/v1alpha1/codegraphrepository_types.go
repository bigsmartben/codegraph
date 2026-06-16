package v1alpha1

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionReady   = "Ready"
	ConditionIndexed = "Indexed"

	PhasePending  = "Pending"
	PhaseSyncing  = "Syncing"
	PhaseIndexing = "Indexing"
	PhaseReady    = "Ready"
	PhaseDegraded = "Degraded"
)

type SyncMode string

const (
	SyncModeManual SyncMode = "manual"
)

// CodeGraphRepositorySpec defines the desired state of CodeGraphRepository.
type CodeGraphRepositorySpec struct {
	// RepoID is the stable path and resource identifier.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	RepoID string `json:"repoId"`

	Git GitSpec `json:"git"`

	MCP MCPSpec `json:"mcp"`

	Storage StorageSpec `json:"storage"`

	// +kubebuilder:default={mode:manual}
	Sync SyncSpec `json:"sync,omitempty"`

	// Image overrides the operator default CodeGraph runtime image.
	// +optional
	Image string `json:"image,omitempty"`

	// Resources applies to both runtime and sync/index containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// NodeSelector constrains runtime and sync/index pods.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations apply to runtime and sync/index pods.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Affinity applies to runtime and sync/index pods.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
}

type GitSpec struct {
	// URL is the repository clone URL.
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// Ref is the branch, tag, or commit to index.
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`

	// AuthSecretRef points to credentials used by git.
	// +optional
	AuthSecretRef *corev1.LocalObjectReference `json:"authSecretRef,omitempty"`
}

type MCPSpec struct {
	// Host is the shared external MCP host.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`
	Host string `json:"host"`

	// Path is the external path, normally /mcp/<repoId>.
	// +kubebuilder:validation:Pattern=`^/mcp/[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Path string `json:"path"`
}

type StorageSpec struct {
	// Size is the PVC request for checkout and .codegraph data.
	Size resource.Quantity `json:"size"`

	// StorageClassName selects the storage class.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`
}

type SyncSpec struct {
	// Mode controls repository refresh behavior.
	// +kubebuilder:validation:Enum=manual
	// +kubebuilder:default=manual
	Mode SyncMode `json:"mode,omitempty"`
}

// CodeGraphRepositoryStatus defines the observed state of CodeGraphRepository.
type CodeGraphRepositoryStatus struct {
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	Phase              string `json:"phase,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions   []metav1.Condition `json:"conditions,omitempty"`
	ResolvedRef  string             `json:"resolvedRef,omitempty"`
	LastSyncTime *metav1.Time       `json:"lastSyncTime,omitempty"`
	Endpoint     string             `json:"endpoint,omitempty"`
	ServiceName  string             `json:"serviceName,omitempty"`
	RouteName    string             `json:"routeName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=`.spec.repoId`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type CodeGraphRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CodeGraphRepositorySpec   `json:"spec,omitempty"`
	Status CodeGraphRepositoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type CodeGraphRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CodeGraphRepository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CodeGraphRepository{}, &CodeGraphRepositoryList{})
}

func (r *CodeGraphRepository) Endpoint() string {
	host := strings.TrimRight(r.Spec.MCP.Host, "/")
	path := r.Spec.MCP.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "https://" + host + path
}

func (r *CodeGraphRepository) RuntimeImage(defaultImage string) string {
	if r.Spec.Image != "" {
		return r.Spec.Image
	}
	return defaultImage
}

func (r *CodeGraphRepository) SetCondition(condition metav1.Condition) {
	condition.ObservedGeneration = r.Generation
	apiMeta.SetStatusCondition(&r.Status.Conditions, condition)
}
