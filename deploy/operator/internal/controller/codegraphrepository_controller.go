package controller

import (
	"context"
	"fmt"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	"github.com/colbymchenry/codegraph/deploy/operator/internal/resources"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const defaultGatewayName = "codegraph"

type CodeGraphRepositoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	DefaultImage     string
	RouteMode        string
	GatewayName      string
	GatewayNamespace string
}

func (r *CodeGraphRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var repo codegraphv1alpha1.CodeGraphRepository
	if err := r.Get(ctx, req.NamespacedName, &repo); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !r.supportsRouteMode() {
		err := fmt.Errorf("unsupported route mode %q", r.RouteMode)
		return r.markDegraded(ctx, &repo, "UnsupportedRouteMode", err)
	}

	if err := r.ensure(ctx, &repo, resources.BuildPVC(&repo)); err != nil {
		return r.markDegraded(ctx, &repo, "PVCApplyFailed", err)
	}
	if err := r.ensureJob(ctx, &repo, resources.BuildSyncJob(&repo, r.DefaultImage)); err != nil {
		return r.markDegraded(ctx, &repo, "SyncJobApplyFailed", err)
	}
	if err := r.ensure(ctx, &repo, resources.BuildService(&repo)); err != nil {
		return r.markDegraded(ctx, &repo, "ServiceApplyFailed", err)
	}
	if err := r.ensureRoute(ctx, &repo); err != nil {
		return r.markDegraded(ctx, &repo, "RouteApplyFailed", err)
	}

	return r.markPending(ctx, &repo)
}

func (r *CodeGraphRepositoryReconciler) ensureRoute(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) error {
	switch r.RouteMode {
	case "", "gateway":
		return r.ensure(ctx, repo, resources.BuildHTTPRoute(repo, resources.RouteConfig{
			GatewayName:      r.gatewayName(),
			GatewayNamespace: r.GatewayNamespace,
		}))
	case "ingress":
		return r.ensure(ctx, repo, resources.BuildIngress(repo))
	default:
		return fmt.Errorf("unsupported route mode %q", r.RouteMode)
	}
}

func (r *CodeGraphRepositoryReconciler) supportsRouteMode() bool {
	switch r.RouteMode {
	case "", "gateway", "ingress":
		return true
	default:
		return false
	}
}

func (r *CodeGraphRepositoryReconciler) gatewayName() string {
	if r.GatewayName != "" {
		return r.GatewayName
	}
	return defaultGatewayName
}

func (r *CodeGraphRepositoryReconciler) ensureJob(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, desired *batchv1.Job) error {
	if err := controllerutil.SetControllerReference(repo, desired, r.Scheme); err != nil {
		return err
	}

	var current batchv1.Job
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &current)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	return err
}

func (r *CodeGraphRepositoryReconciler) ensure(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, desired client.Object) error {
	if err := controllerutil.SetControllerReference(repo, desired, r.Scheme); err != nil {
		return err
	}

	current := desired.DeepCopyObject().(client.Object)
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	applyObjectMeta(current, desired)
	applyObjectSpec(current, desired)
	return r.Update(ctx, current)
}

func applyObjectMeta(current client.Object, desired client.Object) {
	current.SetLabels(desired.GetLabels())
	current.SetAnnotations(desired.GetAnnotations())
	current.SetOwnerReferences(desired.GetOwnerReferences())
}

func applyObjectSpec(current client.Object, desired client.Object) {
	switch current := current.(type) {
	case *corev1.PersistentVolumeClaim:
		current.Spec = desired.(*corev1.PersistentVolumeClaim).Spec
	case *corev1.Service:
		clusterIP := current.Spec.ClusterIP
		clusterIPs := current.Spec.ClusterIPs
		ipFamilies := current.Spec.IPFamilies
		ipFamilyPolicy := current.Spec.IPFamilyPolicy
		healthCheckNodePort := current.Spec.HealthCheckNodePort
		current.Spec = desired.(*corev1.Service).Spec
		current.Spec.ClusterIP = clusterIP
		current.Spec.ClusterIPs = clusterIPs
		current.Spec.IPFamilies = ipFamilies
		current.Spec.IPFamilyPolicy = ipFamilyPolicy
		current.Spec.HealthCheckNodePort = healthCheckNodePort
	case *networkingv1.Ingress:
		current.Spec = desired.(*networkingv1.Ingress).Spec
	case *gatewayv1.HTTPRoute:
		current.Spec = desired.(*gatewayv1.HTTPRoute).Spec
	}
}

func (r *CodeGraphRepositoryReconciler) markPending(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) (ctrl.Result, error) {
	names := resources.NamesFor(repo)
	repo.Status.ObservedGeneration = repo.Generation
	repo.Status.Phase = codegraphv1alpha1.PhasePending
	repo.Status.Endpoint = repo.Endpoint()
	repo.Status.ServiceName = names.Service
	repo.Status.RouteName = names.Route
	repo.SetCondition(metav1.Condition{
		Type:    codegraphv1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  "ResourcesApplied",
		Message: "waiting for sync/index job to complete",
	})
	repo.SetCondition(metav1.Condition{
		Type:    codegraphv1alpha1.ConditionIndexed,
		Status:  metav1.ConditionFalse,
		Reason:  "IndexRunning",
		Message: "waiting for sync/index job to complete",
	})

	if err := r.Status().Update(ctx, repo); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CodeGraphRepositoryReconciler) markDegraded(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, reason string, err error) (ctrl.Result, error) {
	repo.Status.ObservedGeneration = repo.Generation
	repo.Status.Phase = codegraphv1alpha1.PhaseDegraded
	repo.SetCondition(metav1.Condition{
		Type:    codegraphv1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	})
	if updateErr := r.Status().Update(ctx, repo); updateErr != nil {
		return ctrl.Result{}, updateErr
	}
	return ctrl.Result{}, err
}

func (r *CodeGraphRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&codegraphv1alpha1.CodeGraphRepository{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Complete(r)
}
