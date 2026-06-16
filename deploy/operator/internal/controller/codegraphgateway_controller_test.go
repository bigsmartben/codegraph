package controller

import (
	"context"
	"testing"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestGatewayReconcileCreatesGatewayResourcesWithHTTPRoute(t *testing.T) {
	ctx := context.Background()
	gateway := controllerGateway()
	reconciler := newGatewayTestReconciler(t, gateway)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gateway)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertGatewayExistsAndOwned(t, ctx, reconciler.Client, &corev1.ConfigMap{}, gateway, "codegraph-team-gateway")
	assertGatewayExistsAndOwned(t, ctx, reconciler.Client, &appsv1.Deployment{}, gateway, "codegraph-team-gateway")
	assertGatewayExistsAndOwned(t, ctx, reconciler.Client, &corev1.Service{}, gateway, "codegraph-team-gateway")
	route := assertGatewayExistsAndOwned(t, ctx, reconciler.Client, &gatewayv1.HTTPRoute{}, gateway, "codegraph-team-gateway")
	if got := string(route.(*gatewayv1.HTTPRoute).Spec.ParentRefs[0].Name); got != "codegraph-gateway" {
		t.Fatalf("gateway parent name = %q", got)
	}

	assertGatewayPendingStatus(t, ctx, reconciler.Client, gateway)
}

func TestGatewayReconcileMarksReadyWhenDeploymentIsReady(t *testing.T) {
	ctx := context.Background()
	gateway := controllerGateway()
	reconciler := newGatewayTestReconciler(t, gateway)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gateway)})
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}

	var deployment appsv1.Deployment
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: gateway.Namespace, Name: "codegraph-team-gateway"}, &deployment); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	deployment.Generation = 2
	if err := reconciler.Update(ctx, &deployment); err != nil {
		t.Fatalf("update deployment generation: %v", err)
	}
	deployment.Status.ObservedGeneration = deployment.Generation
	deployment.Status.UpdatedReplicas = 1
	deployment.Status.AvailableReplicas = 1
	deployment.Status.UnavailableReplicas = 0
	if err := reconciler.Status().Update(ctx, &deployment); err != nil {
		t.Fatalf("update deployment status: %v", err)
	}

	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gateway)})
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	assertGatewayReadyStatus(t, ctx, reconciler.Client, gateway)
}

func TestGatewayReconcileCreatesIngressWhenConfigured(t *testing.T) {
	ctx := context.Background()
	gateway := controllerGateway()
	reconciler := newGatewayTestReconciler(t, gateway)
	reconciler.RouteMode = "ingress"

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gateway)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertGatewayExistsAndOwned(t, ctx, reconciler.Client, &networkingv1.Ingress{}, gateway, "codegraph-team-gateway")

	route := &gatewayv1.HTTPRoute{}
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: gateway.Namespace, Name: "codegraph-team-gateway"}, route)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("HTTPRoute get error = %v, want NotFound", err)
	}
}

func TestGatewayReconcileMarksDegradedWhenRuntimeImageMissing(t *testing.T) {
	ctx := context.Background()
	gateway := controllerGateway()
	reconciler := newGatewayTestReconciler(t, gateway)
	reconciler.DefaultImage = ""

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gateway)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var configMap corev1.ConfigMap
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: gateway.Namespace, Name: "codegraph-team-gateway"}, &configMap)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("configmap get error = %v, want NotFound", err)
	}
	assertGatewayRuntimeImageMissingStatus(t, ctx, reconciler.Client, gateway)
}

func newGatewayTestReconciler(t *testing.T, objects ...client.Object) *CodeGraphGatewayReconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go AddToScheme: %v", err)
	}
	if err := codegraphv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("codegraph AddToScheme: %v", err)
	}
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatalf("gateway Install: %v", err)
	}

	gateway, ok := objects[0].(*codegraphv1alpha1.CodeGraphGateway)
	if !ok {
		t.Fatalf("first test object must be CodeGraphGateway, got %T", objects[0])
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(gateway, &appsv1.Deployment{}).
		Build()

	return &CodeGraphGatewayReconciler{
		Client:           client,
		Scheme:           scheme,
		DefaultImage:     "ghcr.io/acme/codegraph:runtime",
		RouteMode:        "gateway",
		GatewayName:      "codegraph-gateway",
		GatewayNamespace: "platform",
	}
}

func controllerGateway() *codegraphv1alpha1.CodeGraphGateway {
	return &codegraphv1alpha1.CodeGraphGateway{
		TypeMeta: metav1.TypeMeta{
			APIVersion: codegraphv1alpha1.GroupVersion.String(),
			Kind:       "CodeGraphGateway",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "team-gateway",
			Namespace:  "default",
			Generation: 1,
			UID:        types.UID("gateway-uid-123"),
		},
		Spec: codegraphv1alpha1.CodeGraphGatewaySpec{
			Host: "codegraph.example.com",
			Path: "/mcp",
			Repositories: []codegraphv1alpha1.GatewayRepository{
				{RepoID: "api-service", ServiceName: "codegraph-api-service"},
			},
		},
	}
}

func assertGatewayExistsAndOwned(t *testing.T, ctx context.Context, c client.Client, object client.Object, gateway *codegraphv1alpha1.CodeGraphGateway, name string) client.Object {
	t.Helper()

	if err := c.Get(ctx, types.NamespacedName{Namespace: gateway.Namespace, Name: name}, object); err != nil {
		t.Fatalf("get %T: %v", object, err)
	}
	owners := object.GetOwnerReferences()
	if len(owners) != 1 {
		t.Fatalf("%T ownerReferences = %#v", object, owners)
	}
	owner := owners[0]
	if owner.APIVersion != codegraphv1alpha1.GroupVersion.String() || owner.Kind != "CodeGraphGateway" || owner.Name != gateway.Name || owner.UID != gateway.UID {
		t.Fatalf("%T owner reference = %#v", object, owner)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("%T owner controller = %v", object, owner.Controller)
	}
	return object
}

func assertGatewayPendingStatus(t *testing.T, ctx context.Context, c client.Client, gateway *codegraphv1alpha1.CodeGraphGateway) {
	t.Helper()

	updated := assertGatewayStatusNames(t, ctx, c, gateway)
	if updated.Status.Phase != codegraphv1alpha1.PhasePending {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "RuntimeUnavailable" {
		t.Fatalf("Ready condition = %#v", ready)
	}
}

func assertGatewayReadyStatus(t *testing.T, ctx context.Context, c client.Client, gateway *codegraphv1alpha1.CodeGraphGateway) {
	t.Helper()

	updated := assertGatewayStatusNames(t, ctx, c, gateway)
	if updated.Status.Phase != codegraphv1alpha1.PhaseReady {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.Reason != "RuntimeAvailable" {
		t.Fatalf("Ready condition = %#v", ready)
	}
}

func assertGatewayRuntimeImageMissingStatus(t *testing.T, ctx context.Context, c client.Client, gateway *codegraphv1alpha1.CodeGraphGateway) {
	t.Helper()

	updated := assertGatewayStatusNames(t, ctx, c, gateway)
	if updated.Status.Phase != codegraphv1alpha1.PhaseDegraded {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "RuntimeImageMissing" {
		t.Fatalf("Ready condition = %#v", ready)
	}
}

func assertGatewayStatusNames(t *testing.T, ctx context.Context, c client.Client, gateway *codegraphv1alpha1.CodeGraphGateway) codegraphv1alpha1.CodeGraphGateway {
	t.Helper()

	var updated codegraphv1alpha1.CodeGraphGateway
	if err := c.Get(ctx, client.ObjectKeyFromObject(gateway), &updated); err != nil {
		t.Fatalf("get updated gateway: %v", err)
	}
	if updated.Status.ObservedGeneration != gateway.Generation {
		t.Fatalf("observedGeneration = %d", updated.Status.ObservedGeneration)
	}
	if updated.Status.Endpoint != "https://codegraph.example.com/mcp" {
		t.Fatalf("endpoint = %q", updated.Status.Endpoint)
	}
	if updated.Status.ServiceName != "codegraph-team-gateway" {
		t.Fatalf("serviceName = %q", updated.Status.ServiceName)
	}
	if updated.Status.RouteName != "codegraph-team-gateway" {
		t.Fatalf("routeName = %q", updated.Status.RouteName)
	}
	return updated
}
