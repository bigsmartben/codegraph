package resources

import (
	"reflect"
	"strings"
	"testing"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestBuildPVCUsesRepoNameAndStorageRequest(t *testing.T) {
	repo := workloadRepository()

	pvc := BuildPVC(repo)

	if pvc.Name != "codegraph-api-service" {
		t.Fatalf("Name = %q", pvc.Name)
	}
	if pvc.Namespace != "default" {
		t.Fatalf("Namespace = %q", pvc.Namespace)
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Fatalf("AccessModes = %#v", pvc.Spec.AccessModes)
	}
	got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if got.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Fatalf("storage request = %s", got.String())
	}
	assertOwnedByRepository(t, pvc.OwnerReferences)
}

func TestBuildPVCDefaultsStorageWhenUnset(t *testing.T) {
	repo := workloadRepository()
	repo.Spec.Storage.Size = resource.Quantity{}

	pvc := BuildPVC(repo)

	got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if got.Cmp(resource.MustParse("10Gi")) != 0 {
		t.Fatalf("storage request = %s", got.String())
	}
}

func TestBuildServiceUsesMCPPortAndRepoSelector(t *testing.T) {
	repo := workloadRepository()

	service := BuildService(repo)

	if service.Name != "codegraph-api-service" {
		t.Fatalf("Name = %q", service.Name)
	}
	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("Type = %q", service.Spec.Type)
	}
	if service.Spec.Selector["codegraph.dev/repo-id"] != "api-service" {
		t.Fatalf("selector repo id = %q", service.Spec.Selector["codegraph.dev/repo-id"])
	}
	if service.Spec.Selector[WorkloadLabel] != WorkloadRuntime {
		t.Fatalf("selector workload = %q", service.Spec.Selector[WorkloadLabel])
	}
	if len(service.Spec.Ports) != 1 {
		t.Fatalf("len(ports) = %d", len(service.Spec.Ports))
	}
	port := service.Spec.Ports[0]
	if port.Name != "mcp" {
		t.Fatalf("port name = %q", port.Name)
	}
	if port.Port != 3000 {
		t.Fatalf("port = %d", port.Port)
	}
	if port.TargetPort.StrVal != "mcp" {
		t.Fatalf("target port = %#v", port.TargetPort)
	}
	assertOwnedByRepository(t, service.OwnerReferences)
}

func TestBuildServiceSelectorExcludesSyncJobPods(t *testing.T) {
	repo := workloadRepository()

	service := BuildService(repo)
	job := BuildSyncJob(repo, "ghcr.io/acme/codegraph:default")

	if selectorMatchesLabels(service.Spec.Selector, job.Spec.Template.Labels) {
		t.Fatalf("service selector %#v unexpectedly matches sync job pod labels %#v", service.Spec.Selector, job.Spec.Template.Labels)
	}
}

func TestBuildDeploymentRunsHTTPMCPServerWithOverrideImageAndPVC(t *testing.T) {
	repo := workloadRepository()
	repo.Spec.Image = "ghcr.io/acme/codegraph:repo"

	deployment := BuildDeployment(repo, "ghcr.io/acme/codegraph:default")

	if deployment.Name != "codegraph-api-service" {
		t.Fatalf("Name = %q", deployment.Name)
	}
	assertOwnedByRepository(t, deployment.OwnerReferences)
	assertDeploymentSelectorMatchesRepo(t, deployment)
	if deployment.Spec.Selector.MatchLabels[WorkloadLabel] != WorkloadRuntime {
		t.Fatalf("selector workload = %q", deployment.Spec.Selector.MatchLabels[WorkloadLabel])
	}
	if deployment.Spec.Template.Labels[WorkloadLabel] != WorkloadRuntime {
		t.Fatalf("pod workload label = %q", deployment.Spec.Template.Labels[WorkloadLabel])
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 1 {
		t.Fatalf("Replicas = %v", deployment.Spec.Replicas)
	}
	if deployment.Spec.Template.Annotations["codegraph.dev/repository-generation"] != "1" {
		t.Fatalf("pod annotations = %#v", deployment.Spec.Template.Annotations)
	}

	podSpec := deployment.Spec.Template.Spec
	container := onlyContainer(t, podSpec.Containers)
	if container.Image != "ghcr.io/acme/codegraph:repo" {
		t.Fatalf("Image = %q", container.Image)
	}
	gotCommand := append(append([]string{}, container.Command...), container.Args...)
	wantCommand := []string{"codegraph", "serve", "--mcp", "--http", "--host", "0.0.0.0", "--port", "3000", "--path", "/workspace/repo"}
	if !reflect.DeepEqual(gotCommand, wantCommand) {
		t.Fatalf("command = %#v", gotCommand)
	}
	if container.ReadinessProbe == nil || container.ReadinessProbe.TCPSocket == nil {
		t.Fatalf("missing TCP readiness probe")
	}
	if container.ReadinessProbe.HTTPGet != nil {
		t.Fatalf("readiness probe uses HTTP GET: %#v", container.ReadinessProbe.HTTPGet)
	}
	if container.ReadinessProbe.TCPSocket.Port.StrVal != "mcp" {
		t.Fatalf("readiness TCP port = %#v", container.ReadinessProbe.TCPSocket.Port)
	}
	assertWorkspacePVCVolume(t, podSpec)
	assertWorkspaceMount(t, container)
}

func TestBuildSyncJobClonesIndexesAndWritesResolvedRefFile(t *testing.T) {
	repo := workloadRepository()

	job := BuildSyncJob(repo, "ghcr.io/acme/codegraph:default")

	if job.Name != "codegraph-api-service-sync-1" {
		t.Fatalf("Name = %q", job.Name)
	}
	assertOwnedByRepository(t, job.OwnerReferences)
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 1 {
		t.Fatalf("BackoffLimit = %v", job.Spec.BackoffLimit)
	}
	if job.Spec.Template.Labels[WorkloadLabel] != WorkloadSync {
		t.Fatalf("pod workload label = %q", job.Spec.Template.Labels[WorkloadLabel])
	}

	podSpec := job.Spec.Template.Spec
	if podSpec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("RestartPolicy = %q", podSpec.RestartPolicy)
	}
	container := onlyContainer(t, podSpec.Containers)
	if container.Command[0] != "/bin/sh" || container.Command[1] != "-c" {
		t.Fatalf("Command = %#v", container.Command)
	}
	script := container.Args[0]
	if strings.Contains(script, "rm -rf /workspace/repo\n") {
		t.Fatalf("script deletes current repo before replacement:\n%s", script)
	}
	for _, fragment := range []string{
		"rm -rf /workspace/repo-next",
		`git clone "$GIT_URL" /workspace/repo-next`,
		`git -C /workspace/repo-next checkout "$GIT_REF"`,
		"cd /workspace/repo-next",
		"codegraph init",
		"git -C /workspace/repo-next rev-parse HEAD > /workspace/.resolved-ref-next",
		"rm -rf /workspace/repo-previous",
		"if [ -d /workspace/repo ]; then mv /workspace/repo /workspace/repo-previous; fi",
		"mv /workspace/repo-next /workspace/repo",
		"mv /workspace/.resolved-ref-next /workspace/.resolved-ref",
		"if [ -n \"${GIT_USERNAME:-}\" ] && [ -n \"${GIT_PASSWORD:-}\" ]; then",
		"export GIT_ASKPASS=/tmp/codegraph-git-askpass",
		"if [ -f /git-ssh/ssh-privatekey ]; then",
		"cp /git-ssh/ssh-privatekey /tmp/codegraph-ssh-key",
		"chmod 600 /tmp/codegraph-ssh-key",
		"export GIT_SSH_COMMAND=\"ssh -i /tmp/codegraph-ssh-key -o StrictHostKeyChecking=accept-new\"",
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("script missing %q:\n%s", fragment, script)
		}
	}
	if strings.Contains(script, "chmod 600 /git-ssh/ssh-privatekey") {
		t.Fatalf("script chmods read-only secret volume key:\n%s", script)
	}
	if strings.Contains(script, "codegraph index") {
		t.Fatalf("script should not run a second full index:\n%s", script)
	}
	if strings.Count(script, "codegraph init") != 1 {
		t.Fatalf("codegraph init count = %d:\n%s", strings.Count(script, "codegraph init"), script)
	}
	assertEnvValue(t, container.Env, "GIT_URL", "https://github.com/acme/api-service.git")
	assertEnvValue(t, container.Env, "GIT_REF", "main")
	if len(container.EnvFrom) != 1 || container.EnvFrom[0].SecretRef == nil || container.EnvFrom[0].SecretRef.Name != "api-service-git" {
		t.Fatalf("EnvFrom = %#v", container.EnvFrom)
	}
	assertWorkspacePVCVolume(t, podSpec)
	assertWorkspaceMount(t, container)
	assertSSHSecretVolume(t, podSpec, "api-service-git")
	assertSSHSecretMount(t, container)
}

func TestBuildSyncJobWithoutAuthOmitsSecretEnvAndMounts(t *testing.T) {
	repo := workloadRepository()
	repo.Spec.Git.AuthSecretRef = nil

	job := BuildSyncJob(repo, "ghcr.io/acme/codegraph:default")

	podSpec := job.Spec.Template.Spec
	container := onlyContainer(t, podSpec.Containers)
	if len(container.EnvFrom) != 0 {
		t.Fatalf("EnvFrom = %#v", container.EnvFrom)
	}
	for _, volume := range podSpec.Volumes {
		if volume.Name == "git-ssh" {
			t.Fatalf("unexpected git ssh volume: %#v", podSpec.Volumes)
		}
	}
	for _, mount := range container.VolumeMounts {
		if mount.Name == "git-ssh" || mount.MountPath == "/git-ssh" {
			t.Fatalf("unexpected git ssh mount: %#v", container.VolumeMounts)
		}
	}
}

func workloadRepository() *codegraphv1alpha1.CodeGraphRepository {
	return &codegraphv1alpha1.CodeGraphRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "api-service", Namespace: "default", Generation: 1, UID: types.UID("repo-uid-123")},
		Spec: codegraphv1alpha1.CodeGraphRepositorySpec{
			RepoID: "api-service",
			Git: codegraphv1alpha1.GitSpec{
				URL:           "https://github.com/acme/api-service.git",
				Ref:           "main",
				AuthSecretRef: &corev1.LocalObjectReference{Name: "api-service-git"},
			},
			MCP:     codegraphv1alpha1.MCPSpec{Host: "codegraph.example.com", Path: "/mcp/api-service"},
			Storage: codegraphv1alpha1.StorageSpec{Size: resource.MustParse("20Gi")},
		},
	}
}

func assertOwnedByRepository(t *testing.T, owners []metav1.OwnerReference) {
	t.Helper()
	if len(owners) != 1 {
		t.Fatalf("len(ownerReferences) = %d", len(owners))
	}
	owner := owners[0]
	if owner.APIVersion != codegraphv1alpha1.GroupVersion.String() || owner.Kind != "CodeGraphRepository" || owner.Name != "api-service" || owner.UID != types.UID("repo-uid-123") {
		t.Fatalf("owner reference = %#v", owner)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("owner controller = %v", owner.Controller)
	}
}

func assertDeploymentSelectorMatchesRepo(t *testing.T, deployment *appsv1.Deployment) {
	t.Helper()
	if deployment.Spec.Selector == nil {
		t.Fatalf("missing selector")
	}
	if deployment.Spec.Selector.MatchLabels["codegraph.dev/repo-id"] != "api-service" {
		t.Fatalf("selector = %#v", deployment.Spec.Selector.MatchLabels)
	}
	if deployment.Spec.Template.Labels["codegraph.dev/repo-id"] != "api-service" {
		t.Fatalf("pod labels = %#v", deployment.Spec.Template.Labels)
	}
}

func onlyContainer(t *testing.T, containers []corev1.Container) corev1.Container {
	t.Helper()
	if len(containers) != 1 {
		t.Fatalf("len(containers) = %d", len(containers))
	}
	return containers[0]
}

func assertWorkspacePVCVolume(t *testing.T, podSpec corev1.PodSpec) {
	t.Helper()
	for _, volume := range podSpec.Volumes {
		if volume.Name == "workspace" && volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == "codegraph-api-service" {
			return
		}
	}
	t.Fatalf("workspace PVC volume not found: %#v", podSpec.Volumes)
}

func assertWorkspaceMount(t *testing.T, container corev1.Container) {
	t.Helper()
	for _, mount := range container.VolumeMounts {
		if mount.Name == "workspace" && mount.MountPath == "/workspace" {
			return
		}
	}
	t.Fatalf("workspace mount not found: %#v", container.VolumeMounts)
}

func assertSSHSecretVolume(t *testing.T, podSpec corev1.PodSpec, secretName string) {
	t.Helper()
	for _, volume := range podSpec.Volumes {
		if volume.Name == "git-ssh" && volume.Secret != nil && volume.Secret.SecretName == secretName {
			return
		}
	}
	t.Fatalf("git ssh secret volume not found: %#v", podSpec.Volumes)
}

func assertSSHSecretMount(t *testing.T, container corev1.Container) {
	t.Helper()
	for _, mount := range container.VolumeMounts {
		if mount.Name == "git-ssh" && mount.MountPath == "/git-ssh" && mount.ReadOnly {
			return
		}
	}
	t.Fatalf("git ssh secret mount not found: %#v", container.VolumeMounts)
}

func assertEnvValue(t *testing.T, env []corev1.EnvVar, name string, want string) {
	t.Helper()
	for _, item := range env {
		if item.Name == name {
			if item.Value != want {
				t.Fatalf("env %s = %q", name, item.Value)
			}
			return
		}
	}
	t.Fatalf("env %s not found: %#v", name, env)
}

func selectorMatchesLabels(selector map[string]string, labels map[string]string) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}
