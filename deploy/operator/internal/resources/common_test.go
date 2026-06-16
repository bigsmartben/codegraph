package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

func TestNamesForLongRepoIDAreBoundedAndStable(t *testing.T) {
	repoID := strings.Repeat("a", 62) + "x"
	otherRepoID := strings.Repeat("a", 62) + "y"

	names := NamesFor(repository(repoID))
	again := NamesFor(repository(repoID))
	other := NamesFor(repository(otherRepoID))

	for field, name := range map[string]string{
		"Base":       names.Base,
		"PVC":        names.PVC,
		"SyncJob":    names.SyncJob,
		"Deployment": names.Deployment,
		"Service":    names.Service,
		"Route":      names.Route,
	} {
		if len(name) > 63 {
			t.Fatalf("%s length = %d, name = %q", field, len(name), name)
		}
	}

	hash := shortHash(repoID)
	if !strings.Contains(names.Base, hash) {
		t.Fatalf("Base %q does not contain hash %q", names.Base, hash)
	}
	if !strings.Contains(names.SyncJob, hash) {
		t.Fatalf("SyncJob %q does not contain hash %q", names.SyncJob, hash)
	}
	if !strings.HasSuffix(names.SyncJob, "-sync-7") {
		t.Fatalf("SyncJob = %q, want generation suffix", names.SyncJob)
	}
	if names != again {
		t.Fatalf("NamesFor is not stable: %#v != %#v", names, again)
	}
	if names.Base == other.Base {
		t.Fatalf("long repo IDs collided: %q", names.Base)
	}
}

func TestSelectorForIsLabelsSubset(t *testing.T) {
	repo := repository("api-service")
	labels := LabelsFor(repo)
	selector := SelectorFor(repo)

	for key, value := range selector {
		if labels[key] != value {
			t.Fatalf("selector %q=%q not present in labels: %q", key, value, labels[key])
		}
	}
}

func TestOwnerForUsesControllerReference(t *testing.T) {
	repo := repository("api-service")
	repo.UID = types.UID("repo-uid-123")

	owners := OwnerFor(repo)

	if len(owners) != 1 {
		t.Fatalf("len(owners) = %d", len(owners))
	}
	owner := owners[0]
	if owner.APIVersion != codegraphv1alpha1.GroupVersion.String() {
		t.Fatalf("APIVersion = %q", owner.APIVersion)
	}
	if owner.Kind != "CodeGraphRepository" {
		t.Fatalf("Kind = %q", owner.Kind)
	}
	if owner.Name != "api-service" {
		t.Fatalf("Name = %q", owner.Name)
	}
	if owner.UID != types.UID("repo-uid-123") {
		t.Fatalf("UID = %q", owner.UID)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("Controller = %v", owner.Controller)
	}
	if owner.BlockOwnerDeletion == nil || !*owner.BlockOwnerDeletion {
		t.Fatalf("BlockOwnerDeletion = %v", owner.BlockOwnerDeletion)
	}
}

func repository(repoID string) *codegraphv1alpha1.CodeGraphRepository {
	return &codegraphv1alpha1.CodeGraphRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "api-service",
			Namespace:  "default",
			Generation: 7,
		},
		Spec: codegraphv1alpha1.CodeGraphRepositorySpec{
			RepoID: repoID,
		},
	}
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}
