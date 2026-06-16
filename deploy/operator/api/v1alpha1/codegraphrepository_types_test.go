package v1alpha1

import (
	"os"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestEndpointBuildsFromHostAndPath(t *testing.T) {
	repo := &CodeGraphRepository{
		Spec: CodeGraphRepositorySpec{
			RepoID: "api-service",
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

func TestMCPPathHelpersMatchRepoID(t *testing.T) {
	repo := &CodeGraphRepository{
		Spec: CodeGraphRepositorySpec{
			RepoID: "api-service",
			MCP: MCPSpec{
				Path: "/mcp/api-service",
			},
		},
	}

	if got := repo.ExpectedMCPPath(); got != "/mcp/api-service" {
		t.Fatalf("ExpectedMCPPath() = %q", got)
	}
	if !repo.MCPPathMatchesRepoID() {
		t.Fatalf("MCPPathMatchesRepoID() = false, want true")
	}

	repo.Spec.MCP.Path = "/mcp/other-service"
	if repo.MCPPathMatchesRepoID() {
		t.Fatalf("MCPPathMatchesRepoID() = true, want false")
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

func TestAddToSchemeRegistersCodeGraphRepository(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	obj, err := scheme.New(GroupVersion.WithKind("CodeGraphRepository"))
	if err != nil {
		t.Fatalf("scheme.New(CodeGraphRepository) error = %v", err)
	}
	if _, ok := obj.(*CodeGraphRepository); !ok {
		t.Fatalf("scheme.New(CodeGraphRepository) = %T", obj)
	}
}

func TestGeneratedCRDIncludesStructuralSchemaMarkers(t *testing.T) {
	schema := readGeneratedOpenAPISchema(t)

	conditions := schemaProperty(t, schema, "status", "conditions")
	if got := conditions["x-kubernetes-list-type"]; got != "map" {
		t.Fatalf("status.conditions x-kubernetes-list-type = %v", got)
	}
	if got := conditions["x-kubernetes-list-map-keys"]; !reflect.DeepEqual(got, []any{"type"}) {
		t.Fatalf("status.conditions x-kubernetes-list-map-keys = %#v", got)
	}

	sync := schemaProperty(t, schema, "spec", "sync")
	defaultValue, ok := sync["default"].(map[string]any)
	if !ok {
		t.Fatalf("spec.sync default = %T, want map", sync["default"])
	}
	if got := defaultValue["mode"]; got != "manual" {
		t.Fatalf("spec.sync default.mode = %v", got)
	}

	host := schemaProperty(t, schema, "spec", "mcp", "host")
	if got := host["pattern"]; got != `^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$` {
		t.Fatalf("spec.mcp.host pattern = %v", got)
	}

	spec := schemaProperty(t, schema, "spec")
	validations, ok := spec["x-kubernetes-validations"].([]any)
	if !ok {
		t.Fatalf("spec x-kubernetes-validations = %T, want list", spec["x-kubernetes-validations"])
	}
	for _, validation := range validations {
		item, ok := validation.(map[string]any)
		if !ok {
			t.Fatalf("validation = %T, want map", validation)
		}
		if item["rule"] == "self.mcp.path == '/mcp/' + self.repoId" {
			return
		}
	}
	t.Fatalf("missing MCP path validation in %#v", validations)
}

func readGeneratedOpenAPISchema(t *testing.T) map[string]any {
	t.Helper()

	crdBytes, err := os.ReadFile("../../config/crd/codegraph.dev_codegraphrepositories.yaml")
	if err != nil {
		t.Fatalf("read CRD: %v", err)
	}

	var crd map[string]any
	if err := yaml.Unmarshal(crdBytes, &crd); err != nil {
		t.Fatalf("parse CRD: %v", err)
	}

	spec := mapValue(t, crd, "spec")
	versions, ok := spec["versions"].([]any)
	if !ok || len(versions) == 0 {
		t.Fatalf("spec.versions = %#v", spec["versions"])
	}
	firstVersion, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatalf("spec.versions[0] = %T", versions[0])
	}
	return mapValue(t, mapValue(t, firstVersion, "schema"), "openAPIV3Schema")
}

func schemaProperty(t *testing.T, schema map[string]any, path ...string) map[string]any {
	t.Helper()

	current := schema
	for _, segment := range path {
		properties := mapValue(t, current, "properties")
		current = mapValue(t, properties, segment)
	}
	return current
}

func mapValue(t *testing.T, values map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := values[key]
	if !ok {
		t.Fatalf("missing key %q in %#v", key, values)
	}
	mapped, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%q = %T, want map", key, value)
	}
	return mapped
}
