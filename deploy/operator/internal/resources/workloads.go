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
	WorkspaceVolume    = "workspace"
	WorkspaceMountPath = "/workspace"
	RepoPath           = "/workspace/repo"
	MCPPortName        = "mcp"
	MCPPort            = int32(3000)

	gitSSHVolume = "git-ssh"
	gitSSHPath   = "/git-ssh"
)

func BuildPVC(repo *codegraphv1alpha1.CodeGraphRepository) *corev1.PersistentVolumeClaim {
	names := NamesFor(repo)
	storage := repo.Spec.Storage.Size
	if storage.IsZero() {
		storage = resource.MustParse("10Gi")
	}

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.PVC,
			Namespace:       repo.Namespace,
			Labels:          LabelsFor(repo),
			OwnerReferences: OwnerFor(repo),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: repo.Spec.Storage.StorageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storage,
				},
			},
		},
	}
}

func BuildService(repo *codegraphv1alpha1.CodeGraphRepository) *corev1.Service {
	names := NamesFor(repo)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.Service,
			Namespace:       repo.Namespace,
			Labels:          LabelsFor(repo),
			OwnerReferences: OwnerFor(repo),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: SelectorFor(repo),
			Ports: []corev1.ServicePort{
				{
					Name:       MCPPortName,
					Port:       MCPPort,
					TargetPort: intstr.FromString(MCPPortName),
				},
			},
		},
	}
}

func BuildDeployment(repo *codegraphv1alpha1.CodeGraphRepository, defaultImage string) *appsv1.Deployment {
	names := NamesFor(repo)
	labels := LabelsFor(repo)
	selector := SelectorFor(repo)

	podSpec := podSpecFor(repo, []corev1.Container{{
		Name:      "codegraph",
		Image:     repo.RuntimeImage(defaultImage),
		Command:   []string{"codegraph"},
		Args:      []string{"serve", "--mcp", "--http", "--host", "0.0.0.0", "--port", "3000", "--path", RepoPath},
		Resources: repo.Spec.Resources,
		Ports: []corev1.ContainerPort{
			{
				Name:          MCPPortName,
				ContainerPort: MCPPort,
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/mcp",
					Port: intstr.FromString(MCPPortName),
				},
			},
		},
		VolumeMounts: workspaceMounts(),
	}})

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.Deployment,
			Namespace:       repo.Namespace,
			Labels:          labels,
			OwnerReferences: OwnerFor(repo),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}

func BuildSyncJob(repo *codegraphv1alpha1.CodeGraphRepository, defaultImage string) *batchv1.Job {
	names := NamesFor(repo)
	labels := LabelsFor(repo)
	container := corev1.Container{
		Name:      "sync",
		Image:     repo.RuntimeImage(defaultImage),
		Command:   []string{"/bin/sh", "-c"},
		Args:      []string{syncScript()},
		Resources: repo.Spec.Resources,
		Env: []corev1.EnvVar{
			{Name: "GIT_URL", Value: repo.Spec.Git.URL},
			{Name: "GIT_REF", Value: repo.Spec.Git.Ref},
		},
		VolumeMounts: workspaceMounts(),
	}

	volumes := workspaceVolumes(names.PVC)
	if repo.Spec.Git.AuthSecretRef != nil {
		secretName := repo.Spec.Git.AuthSecretRef.Name
		container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
			},
		})
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      gitSSHVolume,
			MountPath: gitSSHPath,
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: gitSSHVolume,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: secretName},
			},
		})
	}

	podSpec := podSpecFor(repo, []corev1.Container{container})
	podSpec.RestartPolicy = corev1.RestartPolicyNever
	podSpec.Volumes = volumes

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.SyncJob,
			Namespace:       repo.Namespace,
			Labels:          labels,
			OwnerReferences: OwnerFor(repo),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: int32Ptr(1),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}

func syncScript() string {
	return `set -eu
rm -rf /workspace/repo
git clone "$GIT_URL" /workspace/repo
git -C /workspace/repo checkout "$GIT_REF"
cd /workspace/repo
codegraph init
codegraph index
git -C /workspace/repo rev-parse HEAD > /workspace/.resolved-ref`
}

func podSpecFor(repo *codegraphv1alpha1.CodeGraphRepository, containers []corev1.Container) corev1.PodSpec {
	return corev1.PodSpec{
		Containers:    containers,
		NodeSelector:  repo.Spec.NodeSelector,
		Tolerations:   repo.Spec.Tolerations,
		Affinity:      repo.Spec.Affinity,
		Volumes:       workspaceVolumes(NamesFor(repo).PVC),
		RestartPolicy: corev1.RestartPolicyAlways,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: boolPtr(true),
			RunAsUser:    int64Ptr(1000),
			RunAsGroup:   int64Ptr(1000),
			FSGroup:      int64Ptr(1000),
		},
	}
}

func workspaceVolumes(claimName string) []corev1.Volume {
	return []corev1.Volume{
		{
			Name: WorkspaceVolume,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claimName},
			},
		},
	}
}

func workspaceMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      WorkspaceVolume,
			MountPath: WorkspaceMountPath,
		},
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func int32Ptr(value int32) *int32 {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}
