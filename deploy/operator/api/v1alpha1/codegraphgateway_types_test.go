package v1alpha1

import (
	"os"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestGatewayEndpointDefaultsToMCPPath(t *testing.T) {
	gateway := &CodeGraphGateway{
		Spec: CodeGraphGatewaySpec{
			Host: "codegraph.example.com",
		},
	}

	if got := gateway.GatewayPath(); got != "/mcp" {
		t.Fatalf("GatewayPath() = %q", got)
	}
	if got := gateway.Endpoint(); got != "https://codegraph.example.com/mcp" {
		t.Fatalf("Endpoint() = %q", got)
	}
}

func TestGatewayEndpointUsesHTTPForLocalAddresses(t *testing.T) {
	gateway := &CodeGraphGateway{
		Spec: CodeGraphGatewaySpec{
			Host: "127.0.0.1",
			Path: "/mcp",
		},
	}

	if got := gateway.Endpoint(); got != "http://127.0.0.1/mcp" {
		t.Fatalf("Endpoint() = %q", got)
	}
}

func TestGatewaySetConditionReplacesSameType(t *testing.T) {
	gateway := &CodeGraphGateway{}
	gateway.SetCondition(metav1.Condition{
		Type:    ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  "Pending",
		Message: "gateway is pending",
	})
	gateway.SetCondition(metav1.Condition{
		Type:    ConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "RuntimeAvailable",
		Message: "gateway is ready",
	})

	if len(gateway.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(gateway.Status.Conditions))
	}
	if gateway.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Fatalf("condition status = %s", gateway.Status.Conditions[0].Status)
	}
}

func TestAddToSchemeRegistersCodeGraphGateway(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	obj, err := scheme.New(GroupVersion.WithKind("CodeGraphGateway"))
	if err != nil {
		t.Fatalf("scheme.New(CodeGraphGateway) error = %v", err)
	}
	if _, ok := obj.(*CodeGraphGateway); !ok {
		t.Fatalf("scheme.New(CodeGraphGateway) = %T", obj)
	}
}

func TestGeneratedGatewayCRDIncludesSchemaMarkers(t *testing.T) {
	schema := readGeneratedGatewayOpenAPISchema(t)

	path := gatewaySchemaProperty(t, schema, "spec", "path")
	if got := path["default"]; got != "/mcp" {
		t.Fatalf("spec.path default = %v", got)
	}
	if got := path["pattern"]; got != `^/[A-Za-z0-9._~!$&'()*+,;=:@/-]*$` {
		t.Fatalf("spec.path pattern = %v", got)
	}

	repositories := gatewaySchemaProperty(t, schema, "spec", "repositories")
	if got := repositories["x-kubernetes-list-type"]; got != "map" {
		t.Fatalf("spec.repositories x-kubernetes-list-type = %v", got)
	}
	if got := repositories["x-kubernetes-list-map-keys"]; !reflect.DeepEqual(got, []any{"repoId"}) {
		t.Fatalf("spec.repositories x-kubernetes-list-map-keys = %#v", got)
	}

	conditions := gatewaySchemaProperty(t, schema, "status", "conditions")
	if got := conditions["x-kubernetes-list-type"]; got != "map" {
		t.Fatalf("status.conditions x-kubernetes-list-type = %v", got)
	}
	if got := conditions["x-kubernetes-list-map-keys"]; !reflect.DeepEqual(got, []any{"type"}) {
		t.Fatalf("status.conditions x-kubernetes-list-map-keys = %#v", got)
	}
}

func readGeneratedGatewayOpenAPISchema(t *testing.T) map[string]any {
	t.Helper()

	crdBytes, err := os.ReadFile("../../config/crd/codegraph.dev_codegraphgateways.yaml")
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

func gatewaySchemaProperty(t *testing.T, schema map[string]any, path ...string) map[string]any {
	t.Helper()

	current := schema
	for _, segment := range path {
		properties := mapValue(t, current, "properties")
		current = mapValue(t, properties, segment)
	}
	return current
}
