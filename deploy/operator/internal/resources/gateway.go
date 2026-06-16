package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	GatewayReposVolume    = "gateway-repos"
	GatewayReposMountPath = "/etc/codegraph-gateway"
	GatewayReposFilePath  = GatewayReposMountPath + "/repos.json"

	gatewayReposHashAnnotation = "codegraph.dev/gateway-repos-hash"
)

type gatewayReposConfig struct {
	RepoID string `json:"repoId"`
	URL    string `json:"url"`
}

func BuildGatewayConfigMap(gateway *codegraphv1alpha1.CodeGraphGateway) *corev1.ConfigMap {
	names := GatewayNamesFor(gateway)
	reposJSON := gatewayReposJSON(gateway)

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.ConfigMap,
			Namespace:       gateway.Namespace,
			Labels:          GatewayLabelsFor(gateway),
			OwnerReferences: GatewayOwnerFor(gateway),
		},
		Data: map[string]string{
			"repos.json": reposJSON,
		},
	}
}

func BuildGatewayService(gateway *codegraphv1alpha1.CodeGraphGateway) *corev1.Service {
	names := GatewayNamesFor(gateway)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.Service,
			Namespace:       gateway.Namespace,
			Labels:          GatewayLabelsFor(gateway),
			OwnerReferences: GatewayOwnerFor(gateway),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: GatewaySelectorFor(gateway),
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

func BuildGatewayDeployment(gateway *codegraphv1alpha1.CodeGraphGateway, image string) *appsv1.Deployment {
	names := GatewayNamesFor(gateway)
	labels := GatewayLabelsFor(gateway)
	selector := GatewaySelectorFor(gateway)
	podLabels := GatewayLabelsFor(gateway)
	podLabels[WorkloadLabel] = WorkloadGateway

	podSpec := gatewayPodSpecFor([]corev1.Container{{
		Name:            "codegraph",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"codegraph"},
		Args:            []string{"serve", "--mcp", "--http", "--host", "0.0.0.0", "--port", "3000", "--gateway-repos", GatewayReposFilePath},
		Ports: []corev1.ContainerPort{
			{
				Name:          MCPPortName,
				ContainerPort: MCPPort,
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromString(MCPPortName),
				},
			},
			FailureThreshold: 3,
			PeriodSeconds:    10,
			SuccessThreshold: 1,
			TimeoutSeconds:   1,
		},
		TerminationMessagePath:   corev1.TerminationMessagePathDefault,
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      GatewayReposVolume,
				MountPath: GatewayReposMountPath,
				ReadOnly:  true,
			},
		},
	}}, names.ConfigMap)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.Deployment,
			Namespace:       gateway.Namespace,
			Labels:          labels,
			OwnerReferences: GatewayOwnerFor(gateway),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:                int32Ptr(1),
			RevisionHistoryLimit:    int32Ptr(10),
			ProgressDeadlineSeconds: int32Ptr(600),
			Selector:                &metav1.LabelSelector{MatchLabels: selector},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: intstrPtr(intstr.FromString("25%")),
					MaxSurge:       intstrPtr(intstr.FromString("25%")),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
					Annotations: map[string]string{
						gatewayReposHashAnnotation: shortGatewayReposHash(gatewayReposJSON(gateway)),
					},
				},
				Spec: podSpec,
			},
		},
	}
}

func gatewayPodSpecFor(containers []corev1.Container, configMapName string) corev1.PodSpec {
	return corev1.PodSpec{
		Containers:  containers,
		Volumes:     gatewayVolumes(configMapName),
		DNSPolicy:   corev1.DNSClusterFirst,
		RestartPolicy: corev1.RestartPolicyAlways,
		SchedulerName: corev1.DefaultSchedulerName,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: boolPtr(true),
			RunAsUser:    int64Ptr(1000),
			RunAsGroup:   int64Ptr(1000),
			FSGroup:      int64Ptr(1000),
		},
		TerminationGracePeriodSeconds: int64Ptr(defaultTerminationGracePeriodSeconds),
	}
}

func gatewayVolumes(configMapName string) []corev1.Volume {
	return []corev1.Volume{
		{
			Name: GatewayReposVolume,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				},
			},
		},
	}
}

func gatewayReposJSON(gateway *codegraphv1alpha1.CodeGraphGateway) string {
	repos := make([]gatewayReposConfig, 0, len(gateway.Spec.Repositories))
	for _, repo := range gateway.Spec.Repositories {
		repos = append(repos, gatewayReposConfig{
			RepoID: repo.RepoID,
			URL:    fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/mcp", repo.ServiceName, gateway.Namespace, MCPPort),
		})
	}
	data, err := json.Marshal(repos)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func shortGatewayReposHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:shortHashLength]
}
