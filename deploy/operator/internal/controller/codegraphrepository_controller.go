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
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
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
		if updateErr := r.patchStatus(ctx, &repo, func() {
			repo.Status.ObservedGeneration = repo.Generation
			repo.Status.Phase = codegraphv1alpha1.PhaseDegraded
			repo.SetCondition(metav1.Condition{
				Type:    codegraphv1alpha1.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "UnsupportedRouteMode",
				Message: err.Error(),
			})
			repo.SetCondition(indexedFalseCondition("UnsupportedRouteMode", err.Error()))
		}); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, nil
	}

	if err := r.ensurePVC(ctx, &repo, resources.BuildPVC(&repo)); err != nil {
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

	job, found, err := r.getSyncJob(ctx, &repo)
	if err != nil {
		return r.markDegraded(ctx, &repo, "SyncJobReadFailed", err)
	}
	if !found {
		return r.markIndexWaiting(ctx, &repo, codegraphv1alpha1.PhasePending, "SyncJobMissing", "waiting for sync/index job to be created")
	}
	if job.Status.Succeeded == 0 {
		if jobTerminalFailed(job) {
			return r.markIndexFailed(ctx, &repo)
		}
		return r.markIndexWaiting(ctx, &repo, codegraphv1alpha1.PhaseIndexing, "IndexRunning", "waiting for sync/index job to complete")
	}

	if err := r.ensure(ctx, &repo, resources.BuildDeployment(&repo, r.DefaultImage)); err != nil {
		return r.markDegradedWithIndexed(ctx, &repo, "DeploymentApplyFailed", err, indexedSucceededCondition())
	}
	deployment, found, err := r.getDeployment(ctx, &repo)
	if err != nil {
		return r.markDegradedWithIndexed(ctx, &repo, "DeploymentReadFailed", err, indexedSucceededCondition())
	}
	if !found || !deploymentRuntimeReady(deployment) {
		return r.markRuntimePending(ctx, &repo)
	}
	return r.markReady(ctx, &repo)
}

func (r *CodeGraphRepositoryReconciler) getSyncJob(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) (*batchv1.Job, bool, error) {
	names := resources.NamesFor(repo)
	var job batchv1.Job
	err := r.Get(ctx, client.ObjectKey{Namespace: repo.Namespace, Name: names.SyncJob}, &job)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &job, true, nil
}

func (r *CodeGraphRepositoryReconciler) getDeployment(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) (*appsv1.Deployment, bool, error) {
	names := resources.NamesFor(repo)
	var deployment appsv1.Deployment
	err := r.Get(ctx, client.ObjectKey{Namespace: repo.Namespace, Name: names.Deployment}, &deployment)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &deployment, true, nil
}

func jobTerminalFailed(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func deploymentRuntimeReady(deployment *appsv1.Deployment) bool {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}

	return deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.UpdatedReplicas >= desired &&
		deployment.Status.AvailableReplicas >= desired &&
		deployment.Status.UnavailableReplicas == 0
}

func (r *CodeGraphRepositoryReconciler) ensureRoute(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) error {
	switch r.RouteMode {
	case "", "gateway":
		if err := r.deleteIfExists(ctx, resources.BuildIngress(repo)); err != nil {
			return err
		}
		return r.ensure(ctx, repo, resources.BuildHTTPRoute(repo, resources.RouteConfig{
			GatewayName:      r.gatewayName(),
			GatewayNamespace: r.GatewayNamespace,
		}))
	case "ingress":
		if err := r.deleteIfExists(ctx, resources.BuildHTTPRoute(repo, resources.RouteConfig{})); err != nil {
			return err
		}
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

func (r *CodeGraphRepositoryReconciler) ensurePVC(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, desired *corev1.PersistentVolumeClaim) error {
	if err := controllerutil.SetControllerReference(repo, desired, r.Scheme); err != nil {
		return err
	}

	var current corev1.PersistentVolumeClaim
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &current)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	applyObjectMeta(&current, desired)
	return r.Update(ctx, &current)
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

func (r *CodeGraphRepositoryReconciler) deleteIfExists(ctx context.Context, object client.Object) error {
	err := r.Delete(ctx, object)
	if apierrors.IsNotFound(err) || apiMeta.IsNoMatchError(err) {
		return nil
	}
	return err
}

func applyObjectMeta(current client.Object, desired client.Object) {
	current.SetLabels(desired.GetLabels())
	current.SetAnnotations(desired.GetAnnotations())
	current.SetOwnerReferences(desired.GetOwnerReferences())
}

func applyObjectSpec(current client.Object, desired client.Object) {
	switch current := current.(type) {
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
	case *appsv1.Deployment:
		current.Spec = desired.(*appsv1.Deployment).Spec
	}
}

func (r *CodeGraphRepositoryReconciler) markIndexWaiting(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, phase string, reason string, message string) (ctrl.Result, error) {
	if err := r.patchStatus(ctx, repo, func() {
		setBaseStatus(repo)
		repo.Status.Phase = phase
		repo.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: message,
		})
		repo.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionIndexed,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: message,
		})
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CodeGraphRepositoryReconciler) markIndexFailed(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) (ctrl.Result, error) {
	if err := r.patchStatus(ctx, repo, func() {
		setBaseStatus(repo)
		repo.Status.Phase = codegraphv1alpha1.PhaseDegraded
		repo.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "IndexFailed",
			Message: "sync/index job failed",
		})
		repo.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionIndexed,
			Status:  metav1.ConditionFalse,
			Reason:  "IndexFailed",
			Message: "sync/index job failed",
		})
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CodeGraphRepositoryReconciler) markRuntimePending(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) (ctrl.Result, error) {
	if err := r.patchStatus(ctx, repo, func() {
		setBaseStatus(repo)
		repo.Status.Phase = codegraphv1alpha1.PhasePending
		repo.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "RuntimeUnavailable",
			Message: "waiting for runtime deployment to become available",
		})
		repo.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionIndexed,
			Status:  metav1.ConditionTrue,
			Reason:  "IndexSucceeded",
			Message: "sync/index job completed",
		})
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CodeGraphRepositoryReconciler) markReady(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository) (ctrl.Result, error) {
	if err := r.patchStatus(ctx, repo, func() {
		setBaseStatus(repo)
		repo.Status.Phase = codegraphv1alpha1.PhaseReady
		repo.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "RuntimeAvailable",
			Message: "runtime deployment is available",
		})
		repo.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionIndexed,
			Status:  metav1.ConditionTrue,
			Reason:  "IndexSucceeded",
			Message: "sync/index job completed",
		})
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func setBaseStatus(repo *codegraphv1alpha1.CodeGraphRepository) {
	names := resources.NamesFor(repo)
	repo.Status.ObservedGeneration = repo.Generation
	repo.Status.Endpoint = repo.Endpoint()
	repo.Status.ServiceName = names.Service
	repo.Status.RouteName = names.Route
}

func (r *CodeGraphRepositoryReconciler) markDegraded(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, reason string, err error) (ctrl.Result, error) {
	return r.markDegradedWithIndexed(ctx, repo, reason, err, indexedFalseCondition(reason, err.Error()))
}

func (r *CodeGraphRepositoryReconciler) markDegradedWithIndexed(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, reason string, err error, indexed metav1.Condition) (ctrl.Result, error) {
	if updateErr := r.patchStatus(ctx, repo, func() {
		setBaseStatus(repo)
		repo.Status.Phase = codegraphv1alpha1.PhaseDegraded
		repo.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: err.Error(),
		})
		repo.SetCondition(indexed)
	}); updateErr != nil {
		return ctrl.Result{}, updateErr
	}
	return ctrl.Result{}, err
}

func indexedFalseCondition(reason string, message string) metav1.Condition {
	return metav1.Condition{
		Type:    codegraphv1alpha1.ConditionIndexed,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	}
}

func indexedSucceededCondition() metav1.Condition {
	return metav1.Condition{
		Type:    codegraphv1alpha1.ConditionIndexed,
		Status:  metav1.ConditionTrue,
		Reason:  "IndexSucceeded",
		Message: "sync/index job completed",
	}
}

func (r *CodeGraphRepositoryReconciler) patchStatus(ctx context.Context, repo *codegraphv1alpha1.CodeGraphRepository, mutate func()) error {
	base := repo.DeepCopy()
	mutate()
	return r.Status().Patch(ctx, repo, client.MergeFrom(base))
}

func (r *CodeGraphRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&codegraphv1alpha1.CodeGraphRepository{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{})

	switch r.RouteMode {
	case "ingress":
		builder = builder.Owns(&networkingv1.Ingress{})
	default:
		builder = builder.Owns(&gatewayv1.HTTPRoute{})
	}

	return builder.Complete(r)
}
