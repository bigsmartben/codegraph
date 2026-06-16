package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AppName                = "codegraph"
	ComponentRepositoryMCP = "repository-mcp"
	WorkloadLabel          = "codegraph.dev/workload"
	WorkloadRuntime        = "runtime"
	WorkloadSync           = "sync"
	maxResourceNameLength  = 63
	shortHashLength        = 8
)

type Names struct {
	Base       string
	PVC        string
	SyncJob    string
	Deployment string
	Service    string
	Route      string
}

func NamesFor(repo *codegraphv1alpha1.CodeGraphRepository) Names {
	base := boundedRepoName(repo.Spec.RepoID, maxResourceNameLength)
	syncSuffix := fmt.Sprintf("-sync-%d", repo.Generation)
	return Names{
		Base:       base,
		PVC:        base,
		SyncJob:    boundedRepoName(repo.Spec.RepoID, maxResourceNameLength-len(syncSuffix)) + syncSuffix,
		Deployment: base,
		Service:    base,
		Route:      base,
	}
}

func LabelsFor(repo *codegraphv1alpha1.CodeGraphRepository) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       AppName,
		"app.kubernetes.io/component":  ComponentRepositoryMCP,
		"app.kubernetes.io/managed-by": "codegraph-operator",
		"codegraph.dev/repo-id":        repo.Spec.RepoID,
	}
}

func SelectorFor(repo *codegraphv1alpha1.CodeGraphRepository) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      AppName,
		"app.kubernetes.io/component": ComponentRepositoryMCP,
		"codegraph.dev/repo-id":       repo.Spec.RepoID,
	}
}

func RuntimeSelectorFor(repo *codegraphv1alpha1.CodeGraphRepository) map[string]string {
	selector := SelectorFor(repo)
	selector[WorkloadLabel] = WorkloadRuntime
	return selector
}

func OwnerFor(repo *codegraphv1alpha1.CodeGraphRepository) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		*metav1.NewControllerRef(repo, codegraphv1alpha1.GroupVersion.WithKind("CodeGraphRepository")),
	}
}

func boundedRepoName(repoID string, maxLength int) string {
	name := "codegraph-" + repoID
	if len(name) <= maxLength {
		return name
	}

	hash := shortRepoHash(repoID)
	keep := maxLength - len(hash) - 1
	if keep < 1 {
		return hash[:maxLength]
	}
	return name[:keep] + "-" + hash
}

func shortRepoHash(repoID string) string {
	sum := sha256.Sum256([]byte(repoID))
	return hex.EncodeToString(sum[:])[:shortHashLength]
}
