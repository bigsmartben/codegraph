package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
