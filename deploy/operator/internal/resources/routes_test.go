package resources

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
)

func TestBuildHTTPRouteRoutesRepositoryMCPPathThroughGateway(t *testing.T) {
	repo := workloadRepository()

	route := BuildHTTPRoute(repo, RouteConfig{GatewayName: "codegraph", GatewayNamespace: "platform"})

	if route.Name != "codegraph-api-service" {
		t.Fatalf("Name = %q", route.Name)
	}
	if route.Namespace != "default" {
		t.Fatalf("Namespace = %q", route.Namespace)
	}
	assertOwnedByRepository(t, route.OwnerReferences)
	if route.Labels["codegraph.dev/repo-id"] != "api-service" {
		t.Fatalf("labels = %#v", route.Labels)
	}
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "codegraph.example.com" {
		t.Fatalf("Hostnames = %#v", route.Spec.Hostnames)
	}
	if len(route.Spec.ParentRefs) != 1 {
		t.Fatalf("ParentRefs = %#v", route.Spec.ParentRefs)
	}
	parent := route.Spec.ParentRefs[0]
	if string(parent.Name) != "codegraph" {
		t.Fatalf("parent name = %q", parent.Name)
	}
	if parent.Namespace == nil || string(*parent.Namespace) != "platform" {
		t.Fatalf("parent namespace = %v", parent.Namespace)
	}
	if len(route.Spec.Rules) != 1 {
		t.Fatalf("Rules = %#v", route.Spec.Rules)
	}
	rule := route.Spec.Rules[0]
	if len(rule.Matches) != 1 || rule.Matches[0].Path == nil {
		t.Fatalf("Matches = %#v", rule.Matches)
	}
	path := rule.Matches[0].Path
	if path.Type == nil || string(*path.Type) != "PathPrefix" {
		t.Fatalf("path type = %v", path.Type)
	}
	if path.Value == nil || *path.Value != "/mcp/api-service" {
		t.Fatalf("path value = %v", path.Value)
	}
	if len(rule.Filters) != 1 || rule.Filters[0].URLRewrite == nil || rule.Filters[0].URLRewrite.Path == nil {
		t.Fatalf("Filters = %#v", rule.Filters)
	}
	rewrite := rule.Filters[0].URLRewrite.Path
	if string(rewrite.Type) != "ReplacePrefixMatch" {
		t.Fatalf("rewrite type = %q", rewrite.Type)
	}
	if rewrite.ReplacePrefixMatch == nil || *rewrite.ReplacePrefixMatch != "/mcp" {
		t.Fatalf("rewrite prefix = %v", rewrite.ReplacePrefixMatch)
	}
	if len(rule.BackendRefs) != 1 {
		t.Fatalf("BackendRefs = %#v", rule.BackendRefs)
	}
	backend := rule.BackendRefs[0]
	if string(backend.Name) != "codegraph-api-service" {
		t.Fatalf("backend name = %q", backend.Name)
	}
	if backend.Port == nil || int32(*backend.Port) != 3000 {
		t.Fatalf("backend port = %v", backend.Port)
	}
}

func TestBuildIngressFallbackRoutesOnlyExactRepositoryMCPEndpointToService(t *testing.T) {
	repo := workloadRepository()

	ingress := BuildIngress(repo)

	if ingress.Name != "codegraph-api-service" {
		t.Fatalf("Name = %q", ingress.Name)
	}
	if ingress.Namespace != "default" {
		t.Fatalf("Namespace = %q", ingress.Namespace)
	}
	assertOwnedByRepository(t, ingress.OwnerReferences)
	if ingress.Labels["codegraph.dev/repo-id"] != "api-service" {
		t.Fatalf("labels = %#v", ingress.Labels)
	}
	if ingress.Annotations["nginx.ingress.kubernetes.io/rewrite-target"] != "/mcp" {
		t.Fatalf("annotations = %#v", ingress.Annotations)
	}
	if ingress.Spec.IngressClassName == nil || *ingress.Spec.IngressClassName != "nginx" {
		t.Fatalf("ingress class = %v", ingress.Spec.IngressClassName)
	}
	if len(ingress.Spec.Rules) != 1 {
		t.Fatalf("Rules = %#v", ingress.Spec.Rules)
	}
	rule := ingress.Spec.Rules[0]
	if rule.Host != "codegraph.example.com" {
		t.Fatalf("host = %q", rule.Host)
	}
	if rule.HTTP == nil || len(rule.HTTP.Paths) != 1 {
		t.Fatalf("HTTP paths = %#v", rule.HTTP)
	}
	path := rule.HTTP.Paths[0]
	if path.Path != "/mcp/api-service" {
		t.Fatalf("path = %q", path.Path)
	}
	if path.PathType == nil || *path.PathType != networkingv1.PathTypeExact {
		t.Fatalf("path type = %v", path.PathType)
	}
	if path.Backend.Service == nil {
		t.Fatalf("backend service is nil")
	}
	service := path.Backend.Service
	if service.Name != "codegraph-api-service" {
		t.Fatalf("service name = %q", service.Name)
	}
	if service.Port.Number != 3000 {
		t.Fatalf("service port = %d", service.Port.Number)
	}
}

func TestBuildHTTPRouteOmitsEmptyGatewayNamespace(t *testing.T) {
	repo := workloadRepository()

	route := BuildHTTPRoute(repo, RouteConfig{GatewayName: "codegraph"})

	if len(route.Spec.ParentRefs) != 1 {
		t.Fatalf("ParentRefs = %#v", route.Spec.ParentRefs)
	}
	if route.Spec.ParentRefs[0].Namespace != nil {
		t.Fatalf("parent namespace = %v", route.Spec.ParentRefs[0].Namespace)
	}
}
