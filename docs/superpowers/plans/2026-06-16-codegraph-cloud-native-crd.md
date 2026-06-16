# CodeGraph Cloud-Native CRD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Kubernetes `CodeGraphRepository` CRD and controller that declaratively clones, indexes, serves, and routes repository-scoped CodeGraph HTTP MCP servers behind one shared path-based MCP address.

**Architecture:** Add a self-contained Go operator under `deploy/operator/` so the existing TypeScript/Node CLI and MCP server remain untouched. The operator reconciles one custom resource into PVC, sync/index Job, Deployment, Service, and either Gateway API `HTTPRoute` or Kubernetes `Ingress`. Resource builder functions are tested without a cluster first; controller reconciliation is then tested with controller-runtime's fake/envtest clients.

**Tech Stack:** Go 1.23, controller-runtime, controller-tools, Kubernetes API machinery, Gateway API, standard Go tests, existing CodeGraph runtime image and CLI command.

---

## File Structure

Create a nested Go module so operator dependencies do not affect the npm package:

- `deploy/operator/go.mod`: Go module and Kubernetes dependencies.
- `deploy/operator/Makefile`: repeatable generate, manifest, test, and build commands.
- `deploy/operator/cmd/manager/main.go`: controller manager entrypoint.
- `deploy/operator/api/v1alpha1/groupversion_info.go`: API group registration.
- `deploy/operator/api/v1alpha1/codegraphrepository_types.go`: CRD spec/status types and validation markers.
- `deploy/operator/internal/resources/`: pure resource builders for names, labels, PVC, Job, Deployment, Service, HTTPRoute, and Ingress.
- `deploy/operator/internal/controller/codegraphrepository_controller.go`: reconcile loop and status updates.
- `deploy/operator/config/crd/`: generated CRD YAML.
- `deploy/operator/config/samples/codegraphrepository.yaml`: sample repository declaration.
- `deploy/operator/README.md`: local development and cluster usage notes.

Do not modify the existing MCP HTTP server in this first pass. The route layer rewrites `/mcp/<repoId>` to pod-local `/mcp`.

## Task 1: Scaffold Operator Module

**Files:**
- Create: `deploy/operator/go.mod`
- Create: `deploy/operator/Makefile`
- Create: `deploy/operator/cmd/manager/main.go`

- [ ] **Step 1: Create the Go module file**

Create `deploy/operator/go.mod`:

```go
module github.com/colbymchenry/codegraph/deploy/operator

go 1.23

require (
	github.com/go-logr/logr v1.4.2
	k8s.io/api v0.32.2
	k8s.io/apimachinery v0.32.2
	k8s.io/client-go v0.32.2
	sigs.k8s.io/controller-runtime v0.20.2
	sigs.k8s.io/gateway-api v1.2.1
)
```

- [ ] **Step 2: Create the operator Makefile**

Create `deploy/operator/Makefile`:

```makefile
SHELL := /bin/sh

.PHONY: tidy generate manifests test build

tidy:
	go mod tidy

generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.2 object paths="./..."

manifests:
	mkdir -p config/crd
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.2 crd:allowDangerousTypes=true paths="./api/..." output:crd:artifacts:config=config/crd

test:
	go test ./...

build:
	go build ./cmd/manager
```

- [ ] **Step 3: Create the manager entrypoint**

Create `deploy/operator/cmd/manager/main.go`:

```go
package main

import (
	"flag"
	"os"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	"github.com/colbymchenry/codegraph/deploy/operator/internal/controller"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(codegraphv1alpha1.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var gatewayName string
	var gatewayNamespace string
	var routeMode string
	var runtimeImage string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&gatewayName, "gateway-name", "codegraph", "Gateway name used when route-mode=gateway.")
	flag.StringVar(&gatewayNamespace, "gateway-namespace", "", "Gateway namespace used when route-mode=gateway. Defaults to each repository namespace.")
	flag.StringVar(&routeMode, "route-mode", "gateway", "Routing mode: gateway or ingress.")
	flag.StringVar(&runtimeImage, "runtime-image", "ghcr.io/colbymchenry/codegraph:latest", "Default CodeGraph runtime image.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{})))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "codegraph.dev",
	})
	if err != nil {
		ctrl.Log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	reconciler := &controller.CodeGraphRepositoryReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Config: controller.Config{
			DefaultImage:     runtimeImage,
			RouteMode:        routeMode,
			GatewayName:      gatewayName,
			GatewayNamespace: gatewayNamespace,
		},
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		ctrl.Log.Error(err, "problem running manager")
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run tidy and confirm the module resolves**

Run:

```bash
cd deploy/operator
go mod tidy
```

Expected: command exits with status 0 and creates `deploy/operator/go.sum`.

- [ ] **Step 5: Run the initial build and capture expected missing packages**

Run:

```bash
cd deploy/operator
go test ./...
```

Expected: FAIL because `api/v1alpha1` and `internal/controller` packages do not exist yet. The failure should be missing local packages, not dependency download errors.

- [ ] **Step 6: Commit scaffold files**

Run:

```bash
git add deploy/operator/go.mod deploy/operator/go.sum deploy/operator/Makefile deploy/operator/cmd/manager/main.go
git commit -m "feat: scaffold codegraph operator module"
```

## Task 2: Define CodeGraphRepository API Types

**Files:**
- Create: `deploy/operator/api/v1alpha1/groupversion_info.go`
- Create: `deploy/operator/api/v1alpha1/codegraphrepository_types.go`
- Create: `deploy/operator/api/v1alpha1/codegraphrepository_types_test.go`

- [ ] **Step 1: Write API type tests**

Create `deploy/operator/api/v1alpha1/codegraphrepository_types_test.go`:

```go
package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestEndpointBuildsFromHostAndPath(t *testing.T) {
	repo := &CodeGraphRepository{
		Spec: CodeGraphRepositorySpec{
			MCP: MCPSpec{
				Host: "codegraph.example.com",
				Path: "/mcp/api-service",
			},
		},
	}

	if got := repo.Endpoint(); got != "https://codegraph.example.com/mcp/api-service" {
		t.Fatalf("Endpoint() = %q", got)
	}
}

func TestDefaultImageUsesSpecImageWhenSet(t *testing.T) {
	repo := &CodeGraphRepository{
		Spec: CodeGraphRepositorySpec{
			Image: "registry.example.com/codegraph:v1",
		},
	}

	if got := repo.RuntimeImage("fallback:image"); got != "registry.example.com/codegraph:v1" {
		t.Fatalf("RuntimeImage() = %q", got)
	}
}

func TestDefaultImageUsesFallbackWhenSpecImageEmpty(t *testing.T) {
	repo := &CodeGraphRepository{}

	if got := repo.RuntimeImage("fallback:image"); got != "fallback:image" {
		t.Fatalf("RuntimeImage() = %q", got)
	}
}

func TestSetConditionReplacesSameType(t *testing.T) {
	repo := &CodeGraphRepository{}
	repo.SetCondition(metav1.Condition{
		Type:    ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  "Pending",
		Message: "runtime is not ready",
	})
	repo.SetCondition(metav1.Condition{
		Type:    ConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "RuntimeAvailable",
		Message: "runtime is ready",
	})

	if len(repo.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(repo.Status.Conditions))
	}
	if repo.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Fatalf("condition status = %s", repo.Status.Conditions[0].Status)
	}
}

func TestStorageSizeUsesQuantity(t *testing.T) {
	size := resource.MustParse("20Gi")
	repo := &CodeGraphRepository{
		Spec: CodeGraphRepositorySpec{
			Storage: StorageSpec{Size: size},
		},
	}

	if repo.Spec.Storage.Size.String() != "20Gi" {
		t.Fatalf("storage size = %s", repo.Spec.Storage.Size.String())
	}
}
```

- [ ] **Step 2: Run tests and verify the API package is missing**

Run:

```bash
cd deploy/operator
go test ./api/v1alpha1
```

Expected: FAIL with undefined names such as `CodeGraphRepository`, `MCPSpec`, and `ConditionReady`.

- [ ] **Step 3: Add group registration**

Create `deploy/operator/api/v1alpha1/groupversion_info.go`:

```go
// Package v1alpha1 contains API Schema definitions for the codegraph v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=codegraph.dev
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion = schema.GroupVersion{Group: "codegraph.dev", Version: "v1alpha1"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	AddToScheme = SchemeBuilder.AddToScheme
)
```

- [ ] **Step 4: Add CodeGraphRepository types**

Create `deploy/operator/api/v1alpha1/codegraphrepository_types.go`:

```go
package v1alpha1

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
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
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	Phase string `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	ResolvedRef string `json:"resolvedRef,omitempty"`
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`
	RouteName string `json:"routeName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=`.spec.repoId`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type CodeGraphRepository struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec CodeGraphRepositorySpec `json:"spec,omitempty"`
	Status CodeGraphRepositoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type CodeGraphRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items []CodeGraphRepository `json:"items"`
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
	metaSetStatusCondition(&r.Status.Conditions, condition)
}

```

- [ ] **Step 5: Fix the missing condition helper before generation**

Edit `deploy/operator/api/v1alpha1/codegraphrepository_types.go` and add this import:

```go
apiMeta "k8s.io/apimachinery/pkg/api/meta"
```

Then replace `metaSetStatusCondition` calls with:

```go
apiMeta.SetStatusCondition(&r.Status.Conditions, condition)
```

- [ ] **Step 6: Generate deepcopy methods**

Run:

```bash
cd deploy/operator
make generate
```

Expected: command exits with status 0 and creates `deploy/operator/api/v1alpha1/zz_generated.deepcopy.go`.

- [ ] **Step 7: Run API tests**

Run:

```bash
cd deploy/operator
go test ./api/v1alpha1
```

Expected: PASS.

- [ ] **Step 8: Generate CRD manifests**

Run:

```bash
cd deploy/operator
make manifests
```

Expected: command exits with status 0 and creates `deploy/operator/config/crd/codegraph.dev_codegraphrepositories.yaml`.

- [ ] **Step 9: Commit API types**

Run:

```bash
git add deploy/operator/api deploy/operator/config/crd deploy/operator/go.mod deploy/operator/go.sum
git commit -m "feat: add codegraph repository crd types"
```

## Task 3: Add Resource Naming and Common Helpers

**Files:**
- Create: `deploy/operator/internal/resources/common.go`
- Create: `deploy/operator/internal/resources/common_test.go`

- [ ] **Step 1: Write common helper tests**

Create `deploy/operator/internal/resources/common_test.go`:

```go
package resources

import (
	"testing"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNamesUseRepoID(t *testing.T) {
	repo := repository("api-service")
	names := NamesFor(repo)

	if names.Base != "codegraph-api-service" {
		t.Fatalf("Base = %q", names.Base)
	}
	if names.Service != "codegraph-api-service" {
		t.Fatalf("Service = %q", names.Service)
	}
	if names.SyncJob != "codegraph-api-service-sync-7" {
		t.Fatalf("SyncJob = %q", names.SyncJob)
	}
}

func TestLabelsIncludeRepoID(t *testing.T) {
	repo := repository("api-service")
	labels := LabelsFor(repo)

	if labels["app.kubernetes.io/name"] != "codegraph" {
		t.Fatalf("missing app label")
	}
	if labels["codegraph.dev/repo-id"] != "api-service" {
		t.Fatalf("repo label = %q", labels["codegraph.dev/repo-id"])
	}
}

func repository(repoID string) *codegraphv1alpha1.CodeGraphRepository {
	return &codegraphv1alpha1.CodeGraphRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-service",
			Namespace: "default",
			Generation: 7,
		},
		Spec: codegraphv1alpha1.CodeGraphRepositorySpec{
			RepoID: repoID,
		},
	}
}
```

- [ ] **Step 2: Run tests and verify package is missing implementation**

Run:

```bash
cd deploy/operator
go test ./internal/resources
```

Expected: FAIL with undefined `NamesFor` and `LabelsFor`.

- [ ] **Step 3: Implement common helpers**

Create `deploy/operator/internal/resources/common.go`:

```go
package resources

import (
	"fmt"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AppName = "codegraph"
	ComponentRepositoryMCP = "repository-mcp"
)

type Names struct {
	Base string
	PVC string
	SyncJob string
	Deployment string
	Service string
	Route string
}

func NamesFor(repo *codegraphv1alpha1.CodeGraphRepository) Names {
	base := "codegraph-" + repo.Spec.RepoID
	return Names{
		Base: base,
		PVC: base,
		SyncJob: fmt.Sprintf("%s-sync-%d", base, repo.Generation),
		Deployment: base,
		Service: base,
		Route: base,
	}
}

func LabelsFor(repo *codegraphv1alpha1.CodeGraphRepository) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name": AppName,
		"app.kubernetes.io/component": ComponentRepositoryMCP,
		"app.kubernetes.io/managed-by": "codegraph-operator",
		"codegraph.dev/repo-id": repo.Spec.RepoID,
	}
}

func SelectorFor(repo *codegraphv1alpha1.CodeGraphRepository) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name": AppName,
		"app.kubernetes.io/component": ComponentRepositoryMCP,
		"codegraph.dev/repo-id": repo.Spec.RepoID,
	}
}

func OwnerFor(repo *codegraphv1alpha1.CodeGraphRepository) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		*metav1.NewControllerRef(repo, codegraphv1alpha1.GroupVersion.WithKind("CodeGraphRepository")),
	}
}
```

- [ ] **Step 4: Run resource tests**

Run:

```bash
cd deploy/operator
go test ./internal/resources
```

Expected: PASS.

- [ ] **Step 5: Commit common helpers**

Run:

```bash
git add deploy/operator/internal/resources
git commit -m "feat: add operator resource helpers"
```

## Task 4: Build PVC, Service, Deployment, and Job Resources

**Files:**
- Create: `deploy/operator/internal/resources/workloads.go`
- Create: `deploy/operator/internal/resources/workloads_test.go`

- [ ] **Step 1: Write workload builder tests**

Create `deploy/operator/internal/resources/workloads_test.go`:

```go
package resources

import (
	"testing"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildPVCRequestsStorage(t *testing.T) {
	repo := workloadRepository()
	pvc := BuildPVC(repo)

	if pvc.Name != "codegraph-api-service" {
		t.Fatalf("pvc name = %q", pvc.Name)
	}
	if got := pvc.Spec.Resources.Requests.Storage().String(); got != "20Gi" {
		t.Fatalf("storage request = %q", got)
	}
}

func TestBuildServiceTargetsMCPPort(t *testing.T) {
	repo := workloadRepository()
	service := BuildService(repo)

	if service.Spec.Ports[0].Port != 3000 {
		t.Fatalf("service port = %d", service.Spec.Ports[0].Port)
	}
	if service.Spec.Selector["codegraph.dev/repo-id"] != "api-service" {
		t.Fatalf("selector repo id missing")
	}
}

func TestBuildDeploymentRunsHTTPMCPServer(t *testing.T) {
	repo := workloadRepository()
	deployment := BuildDeployment(repo, "codegraph:test")
	container := deployment.Spec.Template.Spec.Containers[0]

	expected := []string{"codegraph", "serve", "--mcp", "--http", "--host", "0.0.0.0", "--port", "3000", "--path", "/workspace/repo"}
	for i, want := range expected {
		if container.Args[i] != want {
			t.Fatalf("args[%d] = %q, want %q", i, container.Args[i], want)
		}
	}
	if container.Image != "codegraph:test" {
		t.Fatalf("image = %q", container.Image)
	}
}

func TestBuildSyncJobIncludesGitAndIndexScript(t *testing.T) {
	repo := workloadRepository()
	job := BuildSyncJob(repo, "codegraph:test")
	container := job.Spec.Template.Spec.Containers[0]
	script := container.Args[1]

	contains := []string{
		"git clone \"$GIT_URL\" /workspace/repo",
		"git -C /workspace/repo checkout \"$GIT_REF\"",
		"codegraph init",
		"codegraph index",
		"git -C /workspace/repo rev-parse HEAD > /workspace/.resolved-ref",
	}
	for _, want := range contains {
		if !stringContains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func workloadRepository() *codegraphv1alpha1.CodeGraphRepository {
	return &codegraphv1alpha1.CodeGraphRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "api-service", Namespace: "default", Generation: 1},
		Spec: codegraphv1alpha1.CodeGraphRepositorySpec{
			RepoID: "api-service",
			Git: codegraphv1alpha1.GitSpec{
				URL: "https://github.com/acme/api-service.git",
				Ref: "main",
				AuthSecretRef: &corev1.LocalObjectReference{Name: "api-service-git"},
			},
			MCP: codegraphv1alpha1.MCPSpec{Host: "codegraph.example.com", Path: "/mcp/api-service"},
			Storage: codegraphv1alpha1.StorageSpec{Size: resource.MustParse("20Gi")},
		},
	}
}

func stringContains(haystack string, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && (haystack == needle || stringContains(haystack[1:], needle) || haystack[:len(needle)] == needle)
}
```

- [ ] **Step 2: Run tests and verify builders are missing**

Run:

```bash
cd deploy/operator
go test ./internal/resources
```

Expected: FAIL with undefined `BuildPVC`, `BuildService`, `BuildDeployment`, and `BuildSyncJob`.

- [ ] **Step 3: Implement workload builders**

Create `deploy/operator/internal/resources/workloads.go`:

```go
package resources

import (
	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	WorkspaceVolume = "workspace"
	WorkspaceMountPath = "/workspace"
	RepoPath = "/workspace/repo"
	MCPPortName = "mcp-http"
	MCPPort = int32(3000)
)

func BuildPVC(repo *codegraphv1alpha1.CodeGraphRepository) *corev1.PersistentVolumeClaim {
	names := NamesFor(repo)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: names.PVC,
			Namespace: repo.Namespace,
			Labels: LabelsFor(repo),
			OwnerReferences: OwnerFor(repo),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: repo.Spec.Storage.Size,
				},
			},
		},
	}
	if repo.Spec.Storage.Size.IsZero() {
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("10Gi")
	}
	if repo.Spec.Storage.StorageClassName != nil {
		pvc.Spec.StorageClassName = repo.Spec.Storage.StorageClassName
	}
	return pvc
}

func BuildService(repo *codegraphv1alpha1.CodeGraphRepository) *corev1.Service {
	names := NamesFor(repo)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: names.Service,
			Namespace: repo.Namespace,
			Labels: LabelsFor(repo),
			OwnerReferences: OwnerFor(repo),
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: SelectorFor(repo),
			Ports: []corev1.ServicePort{
				{Name: MCPPortName, Port: MCPPort, TargetPort: intstr.FromInt32(MCPPort)},
			},
		},
	}
}

func BuildDeployment(repo *codegraphv1alpha1.CodeGraphRepository, defaultImage string) *appsv1.Deployment {
	names := NamesFor(repo)
	replicas := int32(1)
	labels := LabelsFor(repo)
	selector := SelectorFor(repo)
	image := repo.RuntimeImage(defaultImage)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: names.Deployment,
			Namespace: repo.Namespace,
			Labels: labels,
			OwnerReferences: OwnerFor(repo),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: selector},
				Spec: podSpec(repo, []corev1.Container{
					{
						Name: "codegraph",
						Image: image,
						Args: []string{"codegraph", "serve", "--mcp", "--http", "--host", "0.0.0.0", "--port", "3000", "--path", RepoPath},
						Ports: []corev1.ContainerPort{{Name: MCPPortName, ContainerPort: MCPPort}},
						Resources: repo.Spec.Resources,
						VolumeMounts: []corev1.VolumeMount{{Name: WorkspaceVolume, MountPath: WorkspaceMountPath}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{Path: "/mcp", Port: intstr.FromString(MCPPortName)},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds: 10,
						},
					},
				}),
			},
		},
	}
}

func BuildSyncJob(repo *codegraphv1alpha1.CodeGraphRepository, defaultImage string) *batchv1.Job {
	names := NamesFor(repo)
	backoffLimit := int32(1)
	image := repo.RuntimeImage(defaultImage)
	spec := podSpec(repo, []corev1.Container{
		{
			Name: "sync-index",
			Image: image,
			Command: []string{"/bin/sh"},
			Args: []string{"-c", syncScript()},
			Env: []corev1.EnvVar{
				{Name: "GIT_URL", Value: repo.Spec.Git.URL},
				{Name: "GIT_REF", Value: repo.Spec.Git.Ref},
			},
			Resources: repo.Spec.Resources,
			VolumeMounts: []corev1.VolumeMount{{Name: WorkspaceVolume, MountPath: WorkspaceMountPath}},
		},
	})
	spec.RestartPolicy = corev1.RestartPolicyNever
	if repo.Spec.Git.AuthSecretRef != nil {
		spec.Containers[0].EnvFrom = []corev1.EnvFromSource{
			{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: *repo.Spec.Git.AuthSecretRef}},
		}
		spec.Volumes = append(spec.Volumes, corev1.Volume{
			Name: "git-ssh",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: repo.Spec.Git.AuthSecretRef.Name,
					Optional: boolPtr(true),
				},
			},
		})
		spec.Containers[0].VolumeMounts = append(spec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name: "git-ssh",
			MountPath: "/git-ssh",
			ReadOnly: true,
		})
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: names.SyncJob,
			Namespace: repo.Namespace,
			Labels: LabelsFor(repo),
			OwnerReferences: OwnerFor(repo),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: SelectorFor(repo)},
				Spec: spec,
			},
		},
	}
}

func podSpec(repo *codegraphv1alpha1.CodeGraphRepository, containers []corev1.Container) corev1.PodSpec {
	return corev1.PodSpec{
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: boolPtr(true),
			RunAsUser: int64Ptr(1000),
			RunAsGroup: int64Ptr(1000),
			FSGroup: int64Ptr(1000),
		},
		Containers: containers,
		Volumes: []corev1.Volume{
			{
				Name: WorkspaceVolume,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: NamesFor(repo).PVC},
				},
			},
		},
		NodeSelector: repo.Spec.NodeSelector,
		Tolerations: repo.Spec.Tolerations,
		Affinity: repo.Spec.Affinity,
	}
}

func syncScript() string {
	return `set -eu
if [ -n "${GIT_USERNAME:-}" ] && [ -n "${GIT_PASSWORD:-}" ]; then
  cat >/tmp/git-askpass <<'EOF'
#!/bin/sh
case "$1" in
*Username*) printf '%s\n' "$GIT_USERNAME" ;;
*Password*) printf '%s\n' "$GIT_PASSWORD" ;;
*) printf '\n' ;;
esac
EOF
  chmod 0700 /tmp/git-askpass
  export GIT_ASKPASS=/tmp/git-askpass
fi
if [ -f /git-ssh/ssh-privatekey ]; then
  chmod 0400 /git-ssh/ssh-privatekey
  export GIT_SSH_COMMAND="ssh -i /git-ssh/ssh-privatekey -o StrictHostKeyChecking=accept-new"
fi
if [ -d /workspace/repo/.git ]; then
  git -C /workspace/repo fetch --all --tags --prune
else
  rm -rf /workspace/repo
  git clone "$GIT_URL" /workspace/repo
fi
git -C /workspace/repo checkout "$GIT_REF"
cd /workspace/repo
codegraph init
codegraph index
git -C /workspace/repo rev-parse HEAD > /workspace/.resolved-ref
`
}

func boolPtr(value bool) *bool { return &value }
func int64Ptr(value int64) *int64 { return &value }
```

- [ ] **Step 4: Replace recursive test helper with standard library**

Edit `deploy/operator/internal/resources/workloads_test.go`: add import `strings`, remove `stringContains`, and replace each call to `stringContains(script, want)` with:

```go
strings.Contains(script, want)
```

- [ ] **Step 5: Run workload tests**

Run:

```bash
cd deploy/operator
go test ./internal/resources
```

Expected: PASS.

- [ ] **Step 6: Commit workload builders**

Run:

```bash
git add deploy/operator/internal/resources
git commit -m "feat: build codegraph repository workloads"
```

## Task 5: Build Gateway API and Ingress Routes

**Files:**
- Create: `deploy/operator/internal/resources/routes.go`
- Create: `deploy/operator/internal/resources/routes_test.go`

- [ ] **Step 1: Write route builder tests**

Create `deploy/operator/internal/resources/routes_test.go`:

```go
package resources

import (
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestBuildHTTPRouteRewritesRepoPathToMCP(t *testing.T) {
	repo := workloadRepository()
	route := BuildHTTPRoute(repo, RouteConfig{GatewayName: "codegraph", GatewayNamespace: "platform"})

	if route.Name != "codegraph-api-service" {
		t.Fatalf("route name = %q", route.Name)
	}
	if string(route.Spec.Hostnames[0]) != "codegraph.example.com" {
		t.Fatalf("hostname = %q", route.Spec.Hostnames[0])
	}
	if string(route.Spec.ParentRefs[0].Name) != "codegraph" {
		t.Fatalf("gateway name = %q", route.Spec.ParentRefs[0].Name)
	}
	if *route.Spec.ParentRefs[0].Namespace != gatewayv1.Namespace("platform") {
		t.Fatalf("gateway namespace = %q", *route.Spec.ParentRefs[0].Namespace)
	}
	filter := route.Spec.Rules[0].Filters[0]
	if filter.URLRewrite.Path.ReplacePrefixMatch == nil || *filter.URLRewrite.Path.ReplacePrefixMatch != "/mcp" {
		t.Fatalf("route rewrite = %#v", filter.URLRewrite.Path)
	}
}

func TestBuildIngressUsesRepoPathAndService(t *testing.T) {
	repo := workloadRepository()
	ingress := BuildIngress(repo)

	if ingress.Spec.Rules[0].Host != "codegraph.example.com" {
		t.Fatalf("ingress host = %q", ingress.Spec.Rules[0].Host)
	}
	path := ingress.Spec.Rules[0].HTTP.Paths[0]
	if path.Path != "/mcp/api-service" {
		t.Fatalf("ingress path = %q", path.Path)
	}
	if path.Backend.Service.Name != "codegraph-api-service" {
		t.Fatalf("backend service = %q", path.Backend.Service.Name)
	}
	if ingress.Annotations["nginx.ingress.kubernetes.io/rewrite-target"] != "/mcp" {
		t.Fatalf("rewrite annotation missing")
	}
}
```

- [ ] **Step 2: Run tests and verify route builders are missing**

Run:

```bash
cd deploy/operator
go test ./internal/resources
```

Expected: FAIL with undefined `BuildHTTPRoute`, `RouteConfig`, and `BuildIngress`.

- [ ] **Step 3: Implement route builders**

Create `deploy/operator/internal/resources/routes.go`:

```go
package resources

import (
	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type RouteConfig struct {
	GatewayName string
	GatewayNamespace string
}

func BuildHTTPRoute(repo *codegraphv1alpha1.CodeGraphRepository, config RouteConfig) *gatewayv1.HTTPRoute {
	names := NamesFor(repo)
	pathType := gatewayv1.PathMatchPathPrefix
	path := repo.Spec.MCP.Path
	rewriteType := gatewayv1.PrefixMatchHTTPPathModifier
	rewritePath := "/mcp"
	servicePort := gatewayv1.PortNumber(MCPPort)
	parentRef := gatewayv1.ParentReference{Name: gatewayv1.ObjectName(config.GatewayName)}
	if config.GatewayNamespace != "" {
		namespace := gatewayv1.Namespace(config.GatewayNamespace)
		parentRef.Namespace = &namespace
	}

	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: names.Route,
			Namespace: repo.Namespace,
			Labels: LabelsFor(repo),
			OwnerReferences: OwnerFor(repo),
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{parentRef},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(repo.Spec.MCP.Host)},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{Path: &gatewayv1.HTTPPathMatch{Type: &pathType, Value: &path}},
					},
					Filters: []gatewayv1.HTTPRouteFilter{
						{
							Type: gatewayv1.HTTPRouteFilterURLRewrite,
							URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
								Path: &gatewayv1.HTTPPathModifier{
									Type: rewriteType,
									ReplacePrefixMatch: &rewritePath,
								},
							},
						},
					},
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: gatewayv1.ObjectName(names.Service),
									Port: &servicePort,
								},
							},
						},
					},
				},
			},
		},
	}
}

func BuildIngress(repo *codegraphv1alpha1.CodeGraphRepository) *networkingv1.Ingress {
	names := NamesFor(repo)
	pathType := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: names.Route,
			Namespace: repo.Namespace,
			Labels: LabelsFor(repo),
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/rewrite-target": "/mcp",
			},
			OwnerReferences: OwnerFor(repo),
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: repo.Spec.MCP.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path: repo.Spec.MCP.Path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: names.Service,
											Port: networkingv1.ServiceBackendPort{Number: MCPPort},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func IntOrStringMCPPort() intstr.IntOrString {
	return intstr.FromInt32(MCPPort)
}
```

- [ ] **Step 4: Remove unused route helper if Go reports it**

Run:

```bash
cd deploy/operator
go test ./internal/resources
```

If Go reports `IntOrStringMCPPort` unused, delete that function from `routes.go`; otherwise leave the file unchanged. Expected final result: PASS.

- [ ] **Step 5: Commit route builders**

Run:

```bash
git add deploy/operator/internal/resources
git commit -m "feat: build codegraph repository routes"
```

## Task 6: Implement Reconciler Resource Creation

**Files:**
- Create: `deploy/operator/internal/controller/codegraphrepository_controller.go`
- Create: `deploy/operator/internal/controller/codegraphrepository_controller_test.go`

- [ ] **Step 1: Write reconcile creation test**

Create `deploy/operator/internal/controller/codegraphrepository_controller_test.go`:

```go
package controller

import (
	"context"
	"testing"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestReconcileCreatesRepositoryResourcesWithGatewayRoute(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	repo := testRepository()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).WithStatusSubresource(repo).Build()
	reconciler := &CodeGraphRepositoryReconciler{
		Client: c,
		Scheme: scheme,
		Config: Config{
			DefaultImage: "codegraph:test",
			RouteMode: "gateway",
			GatewayName: "codegraph",
			GatewayNamespace: "platform",
		},
	}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: repo.Name, Namespace: repo.Namespace}})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	assertExists(t, ctx, c, &corev1.PersistentVolumeClaim{}, "codegraph-api-service")
	assertExists(t, ctx, c, &batchv1.Job{}, "codegraph-api-service-sync-1")
	assertExists(t, ctx, c, &appsv1.Deployment{}, "codegraph-api-service")
	assertExists(t, ctx, c, &corev1.Service{}, "codegraph-api-service")
	assertExists(t, ctx, c, &gatewayv1.HTTPRoute{}, "codegraph-api-service")
}

func TestReconcileCreatesIngressWhenConfigured(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	repo := testRepository()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).WithStatusSubresource(repo).Build()
	reconciler := &CodeGraphRepositoryReconciler{
		Client: c,
		Scheme: scheme,
		Config: Config{DefaultImage: "codegraph:test", RouteMode: "ingress"},
	}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: repo.Name, Namespace: repo.Namespace}})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	assertExists(t, ctx, c, &networkingv1.Ingress{}, "codegraph-api-service")
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := codegraphv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func testRepository() *codegraphv1alpha1.CodeGraphRepository {
	return &codegraphv1alpha1.CodeGraphRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "api-service", Namespace: "default", Generation: 1},
		Spec: codegraphv1alpha1.CodeGraphRepositorySpec{
			RepoID: "api-service",
			Git: codegraphv1alpha1.GitSpec{URL: "https://github.com/acme/api-service.git", Ref: "main"},
			MCP: codegraphv1alpha1.MCPSpec{Host: "codegraph.example.com", Path: "/mcp/api-service"},
			Storage: codegraphv1alpha1.StorageSpec{Size: resource.MustParse("20Gi")},
		},
	}
}

func assertExists(t *testing.T, ctx context.Context, c client.Client, obj client.Object, name string) {
	t.Helper()
	key := types.NamespacedName{Name: name, Namespace: "default"}
	if err := c.Get(ctx, key, obj); err != nil {
		t.Fatalf("expected %T %s to exist: %v", obj, name, err)
	}
}
```

- [ ] **Step 2: Run controller tests and verify reconciler is missing**

Run:

```bash
cd deploy/operator
go test ./internal/controller
```

Expected: FAIL with undefined `CodeGraphRepositoryReconciler` and `Config`.

- [ ] **Step 3: Implement reconciler creation path**

Create `deploy/operator/internal/controller/codegraphrepository_controller.go`:

```go
package controller

import (
	"context"
	"fmt"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	"github.com/colbymchenry/codegraph/deploy/operator/internal/resources"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type Config struct {
	DefaultImage string
	RouteMode string
	GatewayName string
	GatewayNamespace string
}

type CodeGraphRepositoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config Config
}

func (r *CodeGraphRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var repo codegraphv1alpha1.CodeGraphRepository
	if err := r.Get(ctx, req.NamespacedName, &repo); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := r.ensure(ctx, &repo, resources.BuildPVC(&repo)); err != nil {
		return r.markDegraded(ctx, &repo, "PVCApplyFailed", err)
	}
	if err := r.ensure(ctx, &repo, resources.BuildSyncJob(&repo, r.Config.DefaultImage)); err != nil {
		return r.markDegraded(ctx, &repo, "SyncJobApplyFailed", err)
	}
	if err := r.ensure(ctx, &repo, resources.BuildDeployment(&repo, r.Config.DefaultImage)); err != nil {
		return r.markDegraded(ctx, &repo, "DeploymentApplyFailed", err)
	}
	if err := r.ensure(ctx, &repo, resources.BuildService(&repo)); err != nil {
		return r.markDegraded(ctx, &repo, "ServiceApplyFailed", err)
	}
	if err := r.ensureRoute(ctx, &repo); err != nil {
		return r.markDegraded(ctx, &repo, "RouteApplyFailed", err)
	}

	repo.Status.ObservedGeneration = repo.Generation
	repo.Status.Phase = codegraphv1alpha1.PhasePending
	repo.Status.Endpoint = repo.Endpoint()
	repo.Status.ServiceName = resources.NamesFor(&repo).Service
	repo.Status.RouteName = resources.NamesFor(&repo).Route
	repo.SetCondition(metav1.Condition{
		Type: codegraphv1alpha1.ConditionReady,
		Status: metav1.ConditionFalse,
		Reason: "ResourcesApplied",
		Message: "Repository resources are applied and waiting for workload readiness",
	})
	if err := r.Status().Update(ctx, &repo); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *CodeGraphRepositoryReconciler) ensureRoute(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) error {
	switch r.Config.RouteMode {
	case "", "gateway":
		return r.ensure(ctx, repo, resources.BuildHTTPRoute(repo, resources.RouteConfig{
			GatewayName: r.gatewayName(),
			GatewayNamespace: r.Config.GatewayNamespace,
		}))
	case "ingress":
		return r.ensure(ctx, repo, resources.BuildIngress(repo))
	default:
		return fmt.Errorf("unsupported route mode %q", r.Config.RouteMode)
	}
}

func (r *CodeGraphRepositoryReconciler) gatewayName() string {
	if r.Config.GatewayName != "" {
		return r.Config.GatewayName
	}
	return "codegraph"
}

func (r *CodeGraphRepositoryReconciler) ensure(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, desired client.Object) error {
	if err := controllerutil.SetControllerReference(repo, desired, r.Scheme); err != nil {
		return err
	}
	key := client.ObjectKeyFromObject(desired)
	current := desired.DeepCopyObject().(client.Object)
	err := r.Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	desired.SetResourceVersion(current.GetResourceVersion())
	return r.Update(ctx, desired)
}

func (r *CodeGraphRepositoryReconciler) markDegraded(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, reason string, err error) (ctrl.Result, error) {
	repo.Status.ObservedGeneration = repo.Generation
	repo.Status.Phase = codegraphv1alpha1.PhaseDegraded
	repo.SetCondition(metav1.Condition{
		Type: codegraphv1alpha1.ConditionReady,
		Status: metav1.ConditionFalse,
		Reason: reason,
		Message: err.Error(),
	})
	if updateErr := r.Status().Update(ctx, repo); updateErr != nil {
		return ctrl.Result{}, updateErr
	}
	return ctrl.Result{}, err
}

func (r *CodeGraphRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&codegraphv1alpha1.CodeGraphRepository{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Complete(r)
}
```

- [ ] **Step 4: Run controller tests**

Run:

```bash
cd deploy/operator
go test ./internal/controller
```

Expected: PASS.

- [ ] **Step 5: Run all operator tests**

Run:

```bash
cd deploy/operator
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit reconciler creation**

Run:

```bash
git add deploy/operator/internal/controller deploy/operator/cmd/manager/main.go deploy/operator/go.mod deploy/operator/go.sum
git commit -m "feat: reconcile codegraph repository resources"
```

## Task 7: Add Status Transitions for Jobs and Runtime Readiness

**Files:**
- Modify: `deploy/operator/internal/controller/codegraphrepository_controller.go`
- Modify: `deploy/operator/internal/controller/codegraphrepository_controller_test.go`

- [ ] **Step 1: Add status transition tests**

Append to `deploy/operator/internal/controller/codegraphrepository_controller_test.go`:

```go
func TestReconcileMarksReadyWhenJobAndDeploymentAreReady(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	repo := testRepository()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).WithStatusSubresource(repo).Build()
	reconciler := &CodeGraphRepositoryReconciler{
		Client: c,
		Scheme: scheme,
		Config: Config{DefaultImage: "codegraph:test", RouteMode: "ingress"},
	}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: repo.Name, Namespace: repo.Namespace}}); err != nil {
		t.Fatalf("first reconcile returned error: %v", err)
	}

	var job batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Name: "codegraph-api-service-sync-1", Namespace: "default"}, &job); err != nil {
		t.Fatal(err)
	}
	job.Status.Succeeded = 1
	if err := c.Status().Update(ctx, &job); err != nil {
		t.Fatal(err)
	}

	var deployment appsv1.Deployment
	if err := c.Get(ctx, types.NamespacedName{Name: "codegraph-api-service", Namespace: "default"}, &deployment); err != nil {
		t.Fatal(err)
	}
	deployment.Status.AvailableReplicas = 1
	if err := c.Status().Update(ctx, &deployment); err != nil {
		t.Fatal(err)
	}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: repo.Name, Namespace: repo.Namespace}}); err != nil {
		t.Fatalf("second reconcile returned error: %v", err)
	}

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := c.Get(ctx, types.NamespacedName{Name: repo.Name, Namespace: repo.Namespace}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhaseReady {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	if updated.Status.Endpoint != "https://codegraph.example.com/mcp/api-service" {
		t.Fatalf("endpoint = %q", updated.Status.Endpoint)
	}
}

func TestReconcileMarksDegradedWhenJobFails(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	repo := testRepository()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).WithStatusSubresource(repo).Build()
	reconciler := &CodeGraphRepositoryReconciler{
		Client: c,
		Scheme: scheme,
		Config: Config{DefaultImage: "codegraph:test", RouteMode: "ingress"},
	}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: repo.Name, Namespace: repo.Namespace}}); err != nil {
		t.Fatalf("first reconcile returned error: %v", err)
	}

	var job batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Name: "codegraph-api-service-sync-1", Namespace: "default"}, &job); err != nil {
		t.Fatal(err)
	}
	job.Status.Failed = 1
	if err := c.Status().Update(ctx, &job); err != nil {
		t.Fatal(err)
	}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: repo.Name, Namespace: repo.Namespace}}); err != nil {
		t.Fatalf("second reconcile returned error: %v", err)
	}

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := c.Get(ctx, types.NamespacedName{Name: repo.Name, Namespace: repo.Namespace}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhaseDegraded {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
}
```

- [ ] **Step 2: Run tests and verify readiness logic is missing**

Run:

```bash
cd deploy/operator
go test ./internal/controller
```

Expected: FAIL because status remains `Pending`.

- [ ] **Step 3: Add readiness status logic**

Edit `deploy/operator/internal/controller/codegraphrepository_controller.go` and replace the status block after `ensureRoute` with:

```go
phase, readyCondition, indexedCondition, err := r.computeStatus(ctx, &repo)
if err != nil {
	return ctrl.Result{}, err
}
repo.Status.ObservedGeneration = repo.Generation
repo.Status.Phase = phase
repo.Status.Endpoint = repo.Endpoint()
repo.Status.ServiceName = resources.NamesFor(&repo).Service
repo.Status.RouteName = resources.NamesFor(&repo).Route
repo.SetCondition(readyCondition)
repo.SetCondition(indexedCondition)
if err := r.Status().Update(ctx, &repo); err != nil {
	return ctrl.Result{}, err
}
```

Add these methods to the same file:

```go
func (r *CodeGraphRepositoryReconciler) computeStatus(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) (string, metav1.Condition, metav1.Condition, error) {
	names := resources.NamesFor(repo)

	var job batchv1.Job
	if err := r.Get(ctx, client.ObjectKey{Namespace: repo.Namespace, Name: names.SyncJob}, &job); err != nil {
		if apierrors.IsNotFound(err) {
			return codegraphv1alpha1.PhaseSyncing, readyFalse("WaitingForSyncJob", "sync/index job has not been created"), indexedFalse("WaitingForSyncJob", "sync/index job has not been created"), nil
		}
		return "", metav1.Condition{}, metav1.Condition{}, err
	}

	if job.Status.Failed > 0 {
		return codegraphv1alpha1.PhaseDegraded, readyFalse("IndexFailed", "sync/index job failed"), indexedFalse("IndexFailed", "sync/index job failed"), nil
	}
	if job.Status.Succeeded == 0 {
		return codegraphv1alpha1.PhaseIndexing, readyFalse("IndexRunning", "sync/index job is still running"), indexedFalse("IndexRunning", "sync/index job is still running"), nil
	}

	var deployment appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKey{Namespace: repo.Namespace, Name: names.Deployment}, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return codegraphv1alpha1.PhasePending, readyFalse("WaitingForRuntime", "runtime deployment has not been created"), indexedTrue("IndexSucceeded", "repository indexed successfully"), nil
		}
		return "", metav1.Condition{}, metav1.Condition{}, err
	}
	if deployment.Status.AvailableReplicas == 0 {
		return codegraphv1alpha1.PhasePending, readyFalse("RuntimeUnavailable", "runtime deployment has no available replicas"), indexedTrue("IndexSucceeded", "repository indexed successfully"), nil
	}

	return codegraphv1alpha1.PhaseReady, readyTrue("RuntimeAvailable", "MCP endpoint is serving"), indexedTrue("IndexSucceeded", "repository indexed successfully"), nil
}

func readyTrue(reason string, message string) metav1.Condition {
	return metav1.Condition{Type: codegraphv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: reason, Message: message}
}

func readyFalse(reason string, message string) metav1.Condition {
	return metav1.Condition{Type: codegraphv1alpha1.ConditionReady, Status: metav1.ConditionFalse, Reason: reason, Message: message}
}

func indexedTrue(reason string, message string) metav1.Condition {
	return metav1.Condition{Type: codegraphv1alpha1.ConditionIndexed, Status: metav1.ConditionTrue, Reason: reason, Message: message}
}

func indexedFalse(reason string, message string) metav1.Condition {
	return metav1.Condition{Type: codegraphv1alpha1.ConditionIndexed, Status: metav1.ConditionFalse, Reason: reason, Message: message}
}
```

- [ ] **Step 4: Run controller tests**

Run:

```bash
cd deploy/operator
go test ./internal/controller
```

Expected: PASS.

- [ ] **Step 5: Run all operator tests**

Run:

```bash
cd deploy/operator
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit status transitions**

Run:

```bash
git add deploy/operator/internal/controller
git commit -m "feat: report codegraph repository readiness"
```

## Task 8: Add Sample Manifests and Operator Documentation

**Files:**
- Create: `deploy/operator/config/samples/codegraphrepository.yaml`
- Create: `deploy/operator/README.md`

- [ ] **Step 1: Create a sample repository manifest**

Create `deploy/operator/config/samples/codegraphrepository.yaml`:

```yaml
apiVersion: codegraph.dev/v1alpha1
kind: CodeGraphRepository
metadata:
  name: api-service
  namespace: codegraph
spec:
  repoId: api-service
  git:
    url: https://github.com/acme/api-service.git
    ref: main
    authSecretRef:
      name: api-service-git
  mcp:
    host: codegraph.example.com
    path: /mcp/api-service
  storage:
    size: 20Gi
  sync:
    mode: manual
```

- [ ] **Step 2: Create operator README**

Create `deploy/operator/README.md`:

```markdown
# CodeGraph Operator

This operator manages repository-scoped CodeGraph HTTP MCP servers with a `CodeGraphRepository` custom resource.

## Repository Resource

Each resource owns one repository checkout, one `.codegraph` index, one runtime Deployment, one Service, and one route.

```yaml
apiVersion: codegraph.dev/v1alpha1
kind: CodeGraphRepository
metadata:
  name: api-service
spec:
  repoId: api-service
  git:
    url: https://github.com/acme/api-service.git
    ref: main
  mcp:
    host: codegraph.example.com
    path: /mcp/api-service
  storage:
    size: 20Gi
  sync:
    mode: manual
```

## MCP Address

Clients connect to the shared host and repository path:

```text
https://codegraph.example.com/mcp/api-service
```

The cluster route rewrites that external path to the pod-local CodeGraph MCP path:

```text
/mcp
```

## Local Development

```bash
cd deploy/operator
make tidy
make generate
make manifests
make test
```

## Git Credentials

For HTTPS repositories, create a Secret with `GIT_USERNAME` and `GIT_PASSWORD` keys. For SSH repositories, create a Secret containing `ssh-privatekey`. Reference the Secret with `spec.git.authSecretRef.name`.

## Routing Modes

The manager supports two route modes:

```bash
--route-mode=gateway
--route-mode=ingress
```

Gateway mode creates Gateway API `HTTPRoute` resources. Ingress mode creates standard Kubernetes `Ingress` resources with an nginx rewrite annotation.
```

- [ ] **Step 3: Regenerate manifests and run docs-adjacent tests**

Run:

```bash
cd deploy/operator
make manifests
go test ./...
```

Expected: both commands exit with status 0.

- [ ] **Step 4: Commit documentation and samples**

Run:

```bash
git add deploy/operator/README.md deploy/operator/config/samples deploy/operator/config/crd
git commit -m "docs: add codegraph operator samples"
```

## Task 9: Add Root-Level Validation Entry Point

**Files:**
- Modify: `package.json`
- Modify: `README.md` or `deploy/operator/README.md`

- [ ] **Step 1: Add npm script test expectation**

Before changing `package.json`, run:

```bash
npm run test:operator
```

Expected: FAIL because `test:operator` is not defined.

- [ ] **Step 2: Add operator test script**

Edit `package.json` and add this script entry after `test:eval`:

```json
"test:operator": "cd deploy/operator && go test ./...",
```

Keep the existing JSON comma rules valid.

- [ ] **Step 3: Document root-level validation**

Append this section to `deploy/operator/README.md`:

```markdown
## Root Validation

From the repository root:

```bash
npm run test:operator
```

This runs the Go operator tests without running the full TypeScript test suite.
```

- [ ] **Step 4: Run root operator test**

Run:

```bash
npm run test:operator
```

Expected: PASS with `go test ./...` output from `deploy/operator`.

- [ ] **Step 5: Run existing TypeScript tests that should remain unaffected**

Run:

```bash
npm test -- __tests__/mcp-http.test.ts
```

Expected: PASS. This confirms the cloud-native work did not alter the existing HTTP MCP server behavior.

- [ ] **Step 6: Commit root validation wiring**

Run:

```bash
git add package.json deploy/operator/README.md
git commit -m "test: add operator validation script"
```

## Task 10: Final Verification

**Files:**
- Read: `docs/superpowers/specs/2026-06-16-codegraph-cloud-native-crd-design.md`
- Read: `deploy/operator/README.md`
- Read: `deploy/operator/config/crd/codegraph.dev_codegraphrepositories.yaml`

- [ ] **Step 1: Run complete operator verification**

Run:

```bash
cd deploy/operator
make generate
make manifests
go test ./...
```

Expected: all commands exit with status 0 and no generated file diff appears from generation.

- [ ] **Step 2: Run root validation**

Run:

```bash
npm run test:operator
npm test -- __tests__/mcp-http.test.ts
```

Expected: both commands pass.

- [ ] **Step 3: Inspect generated CRD for required fields**

Run:

```bash
rg -n "repoId|authSecretRef|resolvedRef|endpoint|conditions|/mcp" deploy/operator/config/crd/codegraph.dev_codegraphrepositories.yaml
```

Expected: output includes schema entries for `repoId`, `authSecretRef`, `endpoint`, and `conditions`.

- [ ] **Step 4: Inspect git status**

Run:

```bash
git status --short
```

Expected: clean except for user-owned files that existed before implementation, such as untracked `AGENTS.md` or `.agents/`.

- [ ] **Step 5: Prepare final summary**

Report:

- Operator module path: `deploy/operator`.
- CRD kind: `CodeGraphRepository`.
- Route modes: Gateway API and Ingress.
- Runtime command: `codegraph serve --mcp --http --host 0.0.0.0 --port 3000 --path /workspace/repo`.
- Verification commands and pass/fail results.

## Spec Coverage Review

- Shared path address `https://<host>/mcp/<repoId>`: covered by API fields, route builders, sample YAML, and README.
- No Python proxy: covered by architecture and no proxy files.
- CRD full lifecycle: covered by API types, PVC, sync/index Job, Deployment, Service, route, and status conditions.
- Per-repository isolation: covered by deterministic per-repo names, labels, owner refs, PVC, and Deployment.
- Existing CodeGraph HTTP MCP server reuse: covered by Deployment command and unchanged TypeScript server.
- Failure handling: covered by degraded status on resource apply failure and failed sync/index Job.
- Testing: covered by API, resource builder, controller, root operator script, and HTTP MCP regression test.
