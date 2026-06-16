package v1alpha1

import (
	"net"
	"strings"

	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CodeGraphGatewaySpec defines the desired state of CodeGraphGateway.
type CodeGraphGatewaySpec struct {
	// Host is the shared external MCP host.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`
	Host string `json:"host"`

	// Path is the shared external MCP path exposed by the gateway.
	// +kubebuilder:default=/mcp
	// +kubebuilder:validation:Pattern=`^/[A-Za-z0-9._~!$&'()*+,;=:@/-]*$`
	Path string `json:"path"`

	// Repositories are the backend CodeGraph MCP services served by this gateway.
	// +listType=map
	// +listMapKey=repoId
	Repositories []GatewayRepository `json:"repositories"`
}

type GatewayRepository struct {
	// RepoID is the stable identifier used to prefix tools exposed by the gateway.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	RepoID string `json:"repoId"`

	// ServiceName is the in-cluster service name for the repository runtime.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	ServiceName string `json:"serviceName"`
}

// CodeGraphGatewayStatus defines the observed state of CodeGraphGateway.
type CodeGraphGatewayStatus struct {
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	Phase              string `json:"phase,omitempty"`
	Endpoint           string `json:"endpoint,omitempty"`
	ServiceName        string `json:"serviceName,omitempty"`
	RouteName          string `json:"routeName,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type CodeGraphGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CodeGraphGatewaySpec   `json:"spec,omitempty"`
	Status CodeGraphGatewayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type CodeGraphGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CodeGraphGateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CodeGraphGateway{}, &CodeGraphGatewayList{})
}

func (g *CodeGraphGateway) GatewayPath() string {
	path := g.Spec.Path
	if path == "" {
		path = "/mcp"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func (g *CodeGraphGateway) Endpoint() string {
	host := strings.TrimRight(g.Spec.Host, "/")
	scheme := "https"
	if isLocalHTTPHost(host) {
		scheme = "http"
	}
	return scheme + "://" + host + g.GatewayPath()
}

func (g *CodeGraphGateway) SetCondition(condition metav1.Condition) {
	condition.ObservedGeneration = g.Generation
	apiMeta.SetStatusCondition(&g.Status.Conditions, condition)
}

func isLocalHTTPHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
