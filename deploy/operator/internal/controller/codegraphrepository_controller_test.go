package controller

import (
	"context"
	"testing"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestReconcileCreatesRepositoryResourcesWithGatewayRoute(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	reconciler := newTestReconciler(t, repo)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertExistsAndOwned(t, ctx, reconciler.Client, &corev1.PersistentVolumeClaim{}, repo, "codegraph-api-service")
	assertExistsAndOwned(t, ctx, reconciler.Client, &batchv1.Job{}, repo, "codegraph-api-service-sync-1")
	assertExistsAndOwned(t, ctx, reconciler.Client, &corev1.Service{}, repo, "codegraph-api-service")
	route := assertExistsAndOwned(t, ctx, reconciler.Client, &gatewayv1.HTTPRoute{}, repo, "codegraph-api-service")
	if got := string(route.(*gatewayv1.HTTPRoute).Spec.ParentRefs[0].Name); got != "codegraph-gateway" {
		t.Fatalf("gateway parent name = %q", got)
	}

	deployment := &appsv1.Deployment{}
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, deployment)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("deployment get error = %v, want NotFound", err)
	}

	assertRepositoryPendingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
}

func TestReconcileCreatesIngressWhenConfigured(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	reconciler := newTestReconciler(t, repo)
	reconciler.RouteMode = "ingress"

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertExistsAndOwned(t, ctx, reconciler.Client, &networkingv1.Ingress{}, repo, "codegraph-api-service")

	route := &gatewayv1.HTTPRoute{}
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, route)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("HTTPRoute get error = %v, want NotFound", err)
	}

	assertRepositoryPendingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
}

func TestReconcilePreservesExistingPVCSpec(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	storageClass := "fast"
	existing := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "codegraph-api-service",
			Namespace: "default",
			Labels: map[string]string{
				"stale": "true",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("5Gi"),
				},
			},
		},
	}
	reconciler := newTestReconciler(t, repo, existing)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var pvc corev1.PersistentVolumeClaim
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: "default", Name: "codegraph-api-service"}, &pvc); err != nil {
		t.Fatalf("get pvc: %v", err)
	}
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(resource.MustParse("5Gi")) != 0 {
		t.Fatalf("PVC storage request = %s, want 5Gi", got.String())
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast" {
		t.Fatalf("PVC storageClassName = %v", pvc.Spec.StorageClassName)
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteMany {
		t.Fatalf("PVC accessModes = %#v", pvc.Spec.AccessModes)
	}
	if pvc.Labels["codegraph.dev/repo-id"] != "api-service" || pvc.Labels["stale"] != "" {
		t.Fatalf("PVC labels = %#v", pvc.Labels)
	}
	assertOwnedByRepository(t, pvc.OwnerReferences, repo)
}

func TestReconcileGatewayRouteDeletesExistingIngress(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	existingIngress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "codegraph-api-service",
			Namespace: "default",
		},
	}
	reconciler := newTestReconciler(t, repo, existingIngress)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertExistsAndOwned(t, ctx, reconciler.Client, &gatewayv1.HTTPRoute{}, repo, "codegraph-api-service")
	var ingress networkingv1.Ingress
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: "default", Name: "codegraph-api-service"}, &ingress)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("Ingress get error = %v, want NotFound", err)
	}
}

func TestReconcileIngressDeletesExistingHTTPRoute(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	existingRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "codegraph-api-service",
			Namespace: "default",
		},
	}
	reconciler := newTestReconciler(t, repo, existingRoute)
	reconciler.RouteMode = "ingress"

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertExistsAndOwned(t, ctx, reconciler.Client, &networkingv1.Ingress{}, repo, "codegraph-api-service")
	var route gatewayv1.HTTPRoute
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: "default", Name: "codegraph-api-service"}, &route)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("HTTPRoute get error = %v, want NotFound", err)
	}
}

func TestReconcileMarksUnsupportedRouteModeDegraded(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	reconciler := newTestReconciler(t, repo)
	reconciler.RouteMode = "mesh"

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}

	var updated codegraphv1alpha1.CodeGraphRepository
	if getErr := reconciler.Get(ctx, client.ObjectKeyFromObject(repo), &updated); getErr != nil {
		t.Fatalf("get updated repo: %v", getErr)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhaseDegraded {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "UnsupportedRouteMode" {
		t.Fatalf("Ready condition = %#v", ready)
	}
}

func newTestReconciler(t *testing.T, objects ...client.Object) *CodeGraphRepositoryReconciler {
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

	repo, ok := objects[0].(*codegraphv1alpha1.CodeGraphRepository)
	if !ok {
		t.Fatalf("first test object must be CodeGraphRepository, got %T", objects[0])
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(repo).
		Build()

	return &CodeGraphRepositoryReconciler{
		Client:           client,
		Scheme:           scheme,
		DefaultImage:     "ghcr.io/acme/codegraph:runtime",
		RouteMode:        "gateway",
		GatewayName:      "codegraph-gateway",
		GatewayNamespace: "platform",
	}
}

func controllerRepository() *codegraphv1alpha1.CodeGraphRepository {
	return &codegraphv1alpha1.CodeGraphRepository{
		TypeMeta: metav1.TypeMeta{
			APIVersion: codegraphv1alpha1.GroupVersion.String(),
			Kind:       "CodeGraphRepository",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "api-service",
			Namespace:  "default",
			Generation: 1,
			UID:        types.UID("repo-uid-123"),
		},
		Spec: codegraphv1alpha1.CodeGraphRepositorySpec{
			RepoID: "api-service",
			Git: codegraphv1alpha1.GitSpec{
				URL: "https://github.com/acme/api-service.git",
				Ref: "main",
			},
			MCP: codegraphv1alpha1.MCPSpec{
				Host: "codegraph.example.com",
				Path: "/mcp/api-service",
			},
			Storage: codegraphv1alpha1.StorageSpec{
				Size: resource.MustParse("20Gi"),
			},
		},
	}
}

func assertExistsAndOwned(t *testing.T, ctx context.Context, c client.Client, object client.Object, repo *codegraphv1alpha1.CodeGraphRepository, name string) client.Object {
	t.Helper()

	if err := c.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: name}, object); err != nil {
		t.Fatalf("get %T: %v", object, err)
	}
	owners := object.GetOwnerReferences()
	if len(owners) != 1 {
		t.Fatalf("%T ownerReferences = %#v", object, owners)
	}
	owner := owners[0]
	if owner.APIVersion != codegraphv1alpha1.GroupVersion.String() || owner.Kind != "CodeGraphRepository" || owner.Name != repo.Name || owner.UID != repo.UID {
		t.Fatalf("%T owner reference = %#v", object, owner)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("%T owner controller = %v", object, owner.Controller)
	}
	return object
}

func assertOwnedByRepository(t *testing.T, owners []metav1.OwnerReference, repo *codegraphv1alpha1.CodeGraphRepository) {
	t.Helper()
	if len(owners) != 1 {
		t.Fatalf("ownerReferences = %#v", owners)
	}
	owner := owners[0]
	if owner.APIVersion != codegraphv1alpha1.GroupVersion.String() || owner.Kind != "CodeGraphRepository" || owner.Name != repo.Name || owner.UID != repo.UID {
		t.Fatalf("owner reference = %#v", owner)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("owner controller = %v", owner.Controller)
	}
}

func assertRepositoryPendingStatus(t *testing.T, ctx context.Context, c client.Client, repo *codegraphv1alpha1.CodeGraphRepository, serviceName string, routeName string) {
	t.Helper()

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := c.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.ObservedGeneration != repo.Generation {
		t.Fatalf("observedGeneration = %d", updated.Status.ObservedGeneration)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhasePending {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	if updated.Status.Endpoint != "https://codegraph.example.com/mcp/api-service" {
		t.Fatalf("endpoint = %q", updated.Status.Endpoint)
	}
	if updated.Status.ServiceName != serviceName {
		t.Fatalf("serviceName = %q", updated.Status.ServiceName)
	}
	if updated.Status.RouteName != routeName {
		t.Fatalf("routeName = %q", updated.Status.RouteName)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "ResourcesApplied" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionFalse || indexed.Reason != "IndexRunning" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}
