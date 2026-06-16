package controller

import (
	"context"
	"fmt"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	"github.com/colbymchenry/codegraph/deploy/operator/internal/resources"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type CodeGraphGatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	DefaultImage     string
	RouteMode        string
	GatewayName      string
	GatewayNamespace string
}

func (r *CodeGraphGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var gateway codegraphv1alpha1.CodeGraphGateway
	if err := r.Get(ctx, req.NamespacedName, &gateway); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !r.supportsRouteMode() {
		err := fmt.Errorf("unsupported route mode %q", r.RouteMode)
		if updateErr := r.patchStatus(ctx, &gateway, func() {
			setGatewayBaseStatus(&gateway)
			gateway.Status.Phase = codegraphv1alpha1.PhaseDegraded
			gateway.SetCondition(metav1.Condition{
				Type:    codegraphv1alpha1.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "UnsupportedRouteMode",
				Message: err.Error(),
			})
		}); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, nil
	}
	if r.DefaultImage == "" {
		return r.markMissingRuntimeImage(ctx, &gateway)
	}

	if err := r.ensure(ctx, &gateway, resources.BuildGatewayConfigMap(&gateway)); err != nil {
		return r.markDegraded(ctx, &gateway, "ConfigMapApplyFailed", err)
	}
	if err := r.ensure(ctx, &gateway, resources.BuildGatewayDeployment(&gateway, r.DefaultImage)); err != nil {
		return r.markDegraded(ctx, &gateway, "DeploymentApplyFailed", err)
	}
	if err := r.ensure(ctx, &gateway, resources.BuildGatewayService(&gateway)); err != nil {
		return r.markDegraded(ctx, &gateway, "ServiceApplyFailed", err)
	}
	if err := r.ensureRoute(ctx, &gateway); err != nil {
		return r.markDegraded(ctx, &gateway, "RouteApplyFailed", err)
	}

	deployment, found, err := r.getDeployment(ctx, &gateway)
	if err != nil {
		return r.markDegraded(ctx, &gateway, "DeploymentReadFailed", err)
	}
	if !found || !deploymentRuntimeReady(deployment) {
		return r.markRuntimePending(ctx, &gateway)
	}
	return r.markReady(ctx, &gateway)
}

func (r *CodeGraphGatewayReconciler) getDeployment(ctx context.Context, gateway *codegraphv1alpha1.CodeGraphGateway) (*appsv1.Deployment, bool, error) {
	names := resources.GatewayNamesFor(gateway)
	var deployment appsv1.Deployment
	err := r.Get(ctx, client.ObjectKey{Namespace: gateway.Namespace, Name: names.Deployment}, &deployment)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &deployment, true, nil
}

func (r *CodeGraphGatewayReconciler) ensureRoute(ctx context.Context, gateway *codegraphv1alpha1.CodeGraphGateway) error {
	switch r.RouteMode {
	case "", "gateway":
		if err := r.deleteIfExists(ctx, resources.BuildGatewayIngress(gateway)); err != nil {
			return err
		}
		return r.ensure(ctx, gateway, resources.BuildGatewayHTTPRoute(gateway, resources.RouteConfig{
			GatewayName:      r.gatewayName(),
			GatewayNamespace: r.GatewayNamespace,
		}))
	case "ingress":
		if err := r.deleteIfExists(ctx, resources.BuildGatewayHTTPRoute(gateway, resources.RouteConfig{})); err != nil {
			return err
		}
		return r.ensure(ctx, gateway, resources.BuildGatewayIngress(gateway))
	default:
		return fmt.Errorf("unsupported route mode %q", r.RouteMode)
	}
}

func (r *CodeGraphGatewayReconciler) ensure(ctx context.Context, gateway *codegraphv1alpha1.CodeGraphGateway, desired client.Object) error {
	if err := controllerutil.SetControllerReference(gateway, desired, r.Scheme); err != nil {
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

	before := current.DeepCopyObject().(client.Object)
	applyObjectMeta(current, desired)
	applyObjectSpec(current, desired)
	if apiequality.Semantic.DeepEqual(before, current) {
		return nil
	}
	return r.Update(ctx, current)
}

func (r *CodeGraphGatewayReconciler) deleteIfExists(ctx context.Context, object client.Object) error {
	err := r.Delete(ctx, object)
	if apierrors.IsNotFound(err) || apiMeta.IsNoMatchError(err) {
		return nil
	}
	return err
}

func (r *CodeGraphGatewayReconciler) supportsRouteMode() bool {
	switch r.RouteMode {
	case "", "gateway", "ingress":
		return true
	default:
		return false
	}
}

func (r *CodeGraphGatewayReconciler) gatewayName() string {
	if r.GatewayName != "" {
		return r.GatewayName
	}
	return defaultGatewayName
}

func (r *CodeGraphGatewayReconciler) markRuntimePending(ctx context.Context, gateway *codegraphv1alpha1.CodeGraphGateway) (ctrl.Result, error) {
	if err := r.patchStatus(ctx, gateway, func() {
		setGatewayBaseStatus(gateway)
		gateway.Status.Phase = codegraphv1alpha1.PhasePending
		gateway.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "RuntimeUnavailable",
			Message: "waiting for gateway deployment to become available",
		})
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CodeGraphGatewayReconciler) markReady(ctx context.Context, gateway *codegraphv1alpha1.CodeGraphGateway) (ctrl.Result, error) {
	if err := r.patchStatus(ctx, gateway, func() {
		setGatewayBaseStatus(gateway)
		gateway.Status.Phase = codegraphv1alpha1.PhaseReady
		gateway.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "RuntimeAvailable",
			Message: "gateway deployment is available",
		})
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CodeGraphGatewayReconciler) markMissingRuntimeImage(ctx context.Context, gateway *codegraphv1alpha1.CodeGraphGateway) (ctrl.Result, error) {
	message := "start the controller with --runtime-image"
	if err := r.patchStatus(ctx, gateway, func() {
		setGatewayBaseStatus(gateway)
		gateway.Status.Phase = codegraphv1alpha1.PhaseDegraded
		gateway.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "RuntimeImageMissing",
			Message: message,
		})
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CodeGraphGatewayReconciler) markDegraded(ctx context.Context, gateway *codegraphv1alpha1.CodeGraphGateway, reason string, err error) (ctrl.Result, error) {
	if updateErr := r.patchStatus(ctx, gateway, func() {
		setGatewayBaseStatus(gateway)
		gateway.Status.Phase = codegraphv1alpha1.PhaseDegraded
		gateway.SetCondition(metav1.Condition{
			Type:    codegraphv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: err.Error(),
		})
	}); updateErr != nil {
		return ctrl.Result{}, updateErr
	}
	return ctrl.Result{}, err
}

func setGatewayBaseStatus(gateway *codegraphv1alpha1.CodeGraphGateway) {
	names := resources.GatewayNamesFor(gateway)
	gateway.Status.ObservedGeneration = gateway.Generation
	gateway.Status.Endpoint = gateway.Endpoint()
	gateway.Status.ServiceName = names.Service
	gateway.Status.RouteName = names.Route
}

func (r *CodeGraphGatewayReconciler) patchStatus(ctx context.Context, gateway *codegraphv1alpha1.CodeGraphGateway, mutate func()) error {
	base := gateway.DeepCopy()
	mutate()
	return r.Status().Patch(ctx, gateway, client.MergeFrom(base))
}

func (r *CodeGraphGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&codegraphv1alpha1.CodeGraphGateway{}).
		Owns(&corev1.ConfigMap{}).
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
