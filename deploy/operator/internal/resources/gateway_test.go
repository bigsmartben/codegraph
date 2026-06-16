package resources

import (
	"encoding/json"
	"reflect"
	"testing"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestBuildGatewayConfigMapWritesBackendRepositoryURLs(t *testing.T) {
	gateway := workloadGateway()

	configMap := BuildGatewayConfigMap(gateway)

	if configMap.Name != "codegraph-team-gateway" {
		t.Fatalf("Name = %q", configMap.Name)
	}
	if configMap.Namespace != "default" {
		t.Fatalf("Namespace = %q", configMap.Namespace)
	}
	assertOwnedByGateway(t, configMap.OwnerReferences)

	var repos []map[string]string
	if err := json.Unmarshal([]byte(configMap.Data["repos.json"]), &repos); err != nil {
		t.Fatalf("repos.json parse error = %v, data = %q", err, configMap.Data["repos.json"])
	}
	want := []map[string]string{
		{
			"repoId": "api-service",
			"url":    "http://codegraph-api-service.default.svc.cluster.local:3000/mcp",
		},
		{
			"repoId": "web-client",
			"url":    "http://codegraph-web-client.default.svc.cluster.local:3000/mcp",
		},
	}
	if !reflect.DeepEqual(repos, want) {
		t.Fatalf("repos = %#v", repos)
	}
}

func TestBuildGatewayDeploymentRunsGatewayWithReposConfigMap(t *testing.T) {
	gateway := workloadGateway()

	deployment := BuildGatewayDeployment(gateway, "ghcr.io/acme/codegraph:runtime")

	if deployment.Name != "codegraph-team-gateway" {
		t.Fatalf("Name = %q", deployment.Name)
	}
	assertOwnedByGateway(t, deployment.OwnerReferences)
	if deployment.Spec.Selector.MatchLabels[WorkloadLabel] != WorkloadGateway {
		t.Fatalf("selector workload = %q", deployment.Spec.Selector.MatchLabels[WorkloadLabel])
	}
	if deployment.Spec.Template.Labels[WorkloadLabel] != WorkloadGateway {
		t.Fatalf("pod workload label = %q", deployment.Spec.Template.Labels[WorkloadLabel])
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 1 {
		t.Fatalf("Replicas = %v", deployment.Spec.Replicas)
	}

	podSpec := deployment.Spec.Template.Spec
	container := onlyContainer(t, podSpec.Containers)
	gotCommand := append(append([]string{}, container.Command...), container.Args...)
	wantCommand := []string{"codegraph", "serve", "--mcp", "--http", "--host", "0.0.0.0", "--port", "3000", "--gateway-repos", "/etc/codegraph-gateway/repos.json"}
	if !reflect.DeepEqual(gotCommand, wantCommand) {
		t.Fatalf("command = %#v", gotCommand)
	}
	if container.Image != "ghcr.io/acme/codegraph:runtime" {
		t.Fatalf("Image = %q", container.Image)
	}
	assertGatewayConfigMapVolume(t, podSpec)
	assertGatewayConfigMapMount(t, container)
}

func TestBuildGatewayServiceUsesGatewaySelectorAndMCPPort(t *testing.T) {
	gateway := workloadGateway()

	service := BuildGatewayService(gateway)

	if service.Name != "codegraph-team-gateway" {
		t.Fatalf("Name = %q", service.Name)
	}
	if service.Spec.Selector[WorkloadLabel] != WorkloadGateway {
		t.Fatalf("selector workload = %q", service.Spec.Selector[WorkloadLabel])
	}
	if len(service.Spec.Ports) != 1 {
		t.Fatalf("len(ports) = %d", len(service.Spec.Ports))
	}
	port := service.Spec.Ports[0]
	if port.Name != "mcp" || port.Port != 3000 || port.TargetPort.StrVal != "mcp" {
		t.Fatalf("port = %#v", port)
	}
	assertOwnedByGateway(t, service.OwnerReferences)
}

func TestBuildGatewayHTTPRouteExposesSharedMCPPath(t *testing.T) {
	gateway := workloadGateway()

	route := BuildGatewayHTTPRoute(gateway, RouteConfig{GatewayName: "edge", GatewayNamespace: "platform"})

	if route.Name != "codegraph-team-gateway" {
		t.Fatalf("Name = %q", route.Name)
	}
	assertOwnedByGateway(t, route.OwnerReferences)
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "codegraph.example.com" {
		t.Fatalf("Hostnames = %#v", route.Spec.Hostnames)
	}
	if len(route.Spec.ParentRefs) != 1 || string(route.Spec.ParentRefs[0].Name) != "edge" {
		t.Fatalf("ParentRefs = %#v", route.Spec.ParentRefs)
	}
	if route.Spec.ParentRefs[0].Namespace == nil || string(*route.Spec.ParentRefs[0].Namespace) != "platform" {
		t.Fatalf("parent namespace = %v", route.Spec.ParentRefs[0].Namespace)
	}
	rule := route.Spec.Rules[0]
	path := rule.Matches[0].Path
	if path.Value == nil || *path.Value != "/mcp" {
		t.Fatalf("path value = %v", path.Value)
	}
	if len(rule.Filters) != 0 {
		t.Fatalf("Filters = %#v", rule.Filters)
	}
	if string(rule.BackendRefs[0].Name) != "codegraph-team-gateway" {
		t.Fatalf("backend name = %q", rule.BackendRefs[0].Name)
	}
}

func TestBuildGatewayIngressExposesSharedMCPPath(t *testing.T) {
	gateway := workloadGateway()

	ingress := BuildGatewayIngress(gateway)

	if ingress.Name != "codegraph-team-gateway" {
		t.Fatalf("Name = %q", ingress.Name)
	}
	if ingress.Spec.IngressClassName != nil {
		t.Fatalf("ingress class = %v", *ingress.Spec.IngressClassName)
	}
	assertOwnedByGateway(t, ingress.OwnerReferences)
	if ingress.Annotations[nginxRewriteTargetAnnotation] != "" {
		t.Fatalf("annotations = %#v", ingress.Annotations)
	}
	path := ingress.Spec.Rules[0].HTTP.Paths[0]
	if path.Path != "/mcp" {
		t.Fatalf("path = %q", path.Path)
	}
	if path.PathType == nil || *path.PathType != networkingv1.PathTypeExact {
		t.Fatalf("path type = %v", path.PathType)
	}
	if path.Backend.Service == nil || path.Backend.Service.Name != "codegraph-team-gateway" {
		t.Fatalf("backend service = %#v", path.Backend.Service)
	}
}

func TestBuildGatewayIngressOmitsHostWhenGatewayHostIsLocalIP(t *testing.T) {
	gateway := workloadGateway()
	gateway.Spec.Host = "127.0.0.1"

	ingress := BuildGatewayIngress(gateway)

	if ingress.Spec.Rules[0].Host != "" {
		t.Fatalf("Host = %q", ingress.Spec.Rules[0].Host)
	}
}

func workloadGateway() *codegraphv1alpha1.CodeGraphGateway {
	return &codegraphv1alpha1.CodeGraphGateway{
		ObjectMeta: metav1.ObjectMeta{Name: "team-gateway", Namespace: "default", Generation: 1, UID: types.UID("gateway-uid-123")},
		Spec: codegraphv1alpha1.CodeGraphGatewaySpec{
			Host: "codegraph.example.com",
			Path: "/mcp",
			Repositories: []codegraphv1alpha1.GatewayRepository{
				{RepoID: "api-service", ServiceName: "codegraph-api-service"},
				{RepoID: "web-client", ServiceName: "codegraph-web-client"},
			},
		},
	}
}

func assertOwnedByGateway(t *testing.T, owners []metav1.OwnerReference) {
	t.Helper()
	if len(owners) != 1 {
		t.Fatalf("len(ownerReferences) = %d", len(owners))
	}
	owner := owners[0]
	if owner.APIVersion != codegraphv1alpha1.GroupVersion.String() || owner.Kind != "CodeGraphGateway" || owner.Name != "team-gateway" || owner.UID != types.UID("gateway-uid-123") {
		t.Fatalf("owner reference = %#v", owner)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("owner controller = %v", owner.Controller)
	}
}

func assertGatewayConfigMapVolume(t *testing.T, podSpec corev1.PodSpec) {
	t.Helper()
	for _, volume := range podSpec.Volumes {
		if volume.Name == GatewayReposVolume && volume.ConfigMap != nil && volume.ConfigMap.Name == "codegraph-team-gateway" {
			return
		}
	}
	t.Fatalf("gateway configmap volume not found: %#v", podSpec.Volumes)
}

func assertGatewayConfigMapMount(t *testing.T, container corev1.Container) {
	t.Helper()
	for _, mount := range container.VolumeMounts {
		if mount.Name == GatewayReposVolume && mount.MountPath == GatewayReposMountPath && mount.ReadOnly {
			return
		}
	}
	t.Fatalf("gateway configmap mount not found: %#v", container.VolumeMounts)
}
