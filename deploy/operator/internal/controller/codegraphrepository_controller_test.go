package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	codegraphv1alpha1 "github.com/colbymchenry/codegraph/deploy/operator/api/v1alpha1"
	"github.com/colbymchenry/codegraph/deploy/operator/internal/resources"
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

	assertRepositoryIndexingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
}

func TestReconcileMarksDegradedWhenRuntimeImageMissing(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	reconciler := newTestReconciler(t, repo)
	reconciler.DefaultImage = ""

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var pvc corev1.PersistentVolumeClaim
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, &pvc)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("pvc get error = %v, want NotFound", err)
	}
	var job batchv1.Job
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service-sync-1"}, &job)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("sync job get error = %v, want NotFound", err)
	}
	assertRepositoryRuntimeImageMissingStatus(t, ctx, reconciler.Client, repo)
}

func TestReconcileMarksReadyWhenJobAndDeploymentAreReady(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	reconciler := newTestReconciler(t, repo)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}

	var job batchv1.Job
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service-sync-1"}, &job); err != nil {
		t.Fatalf("get job: %v", err)
	}
	job.Status.Failed = 1
	job.Status.Succeeded = 1
	if err := reconciler.Status().Update(ctx, &job); err != nil {
		t.Fatalf("update job status: %v", err)
	}

	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	assertExistsAndOwned(t, ctx, reconciler.Client, &appsv1.Deployment{}, repo, "codegraph-api-service")
	assertRepositoryRuntimePendingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")

	var deployment appsv1.Deployment
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, &deployment); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	deployment.Generation = 2
	if err := reconciler.Update(ctx, &deployment); err != nil {
		t.Fatalf("update deployment generation: %v", err)
	}
	deployment.Status.AvailableReplicas = 1
	if err := reconciler.Status().Update(ctx, &deployment); err != nil {
		t.Fatalf("update deployment status: %v", err)
	}

	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("third Reconcile() error = %v", err)
	}

	assertRepositoryRuntimePendingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")

	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, &deployment); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	deployment.Status.ObservedGeneration = deployment.Generation
	deployment.Status.UpdatedReplicas = 1
	deployment.Status.AvailableReplicas = 1
	deployment.Status.UnavailableReplicas = 0
	if err := reconciler.Status().Update(ctx, &deployment); err != nil {
		t.Fatalf("update deployment ready status: %v", err)
	}

	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("fourth Reconcile() error = %v", err)
	}

	assertRepositoryReadyStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
}

func TestReconcileDeletesStaleRuntimeBeforeCreatingNextGenerationSyncJob(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	repo.Generation = 2
	staleRuntime := resources.BuildDeployment(repo, "ghcr.io/acme/codegraph:runtime")
	staleRuntime.Spec.Template.Annotations[resources.RepositoryGenerationAnnotation] = "1"
	reconciler := newTestReconciler(t, repo, staleRuntime)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var deployment appsv1.Deployment
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, &deployment)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("deployment get error = %v, want NotFound", err)
	}
	var job batchv1.Job
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service-sync-2"}, &job)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("sync job get error = %v, want NotFound", err)
	}
	assertRepositoryRuntimeShutdownStatus(t, ctx, reconciler.Client, repo)
}

func TestReconcileBlocksNextGenerationSyncWhileStaleRuntimePodExists(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	repo.Generation = 2
	staleRuntimePod := runtimePod(repo, "codegraph-api-service-stale", "1")
	staleRuntimePod.DeletionTimestamp = &metav1.Time{Time: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)}
	staleRuntimePod.Finalizers = []string{"codegraph.dev/test-finalizer"}
	reconciler := newTestReconciler(t, repo, staleRuntimePod)

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatalf("RequeueAfter = %s, want positive duration", result.RequeueAfter)
	}

	var job batchv1.Job
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service-sync-2"}, &job)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("sync job get error = %v, want NotFound", err)
	}
	assertRepositoryRuntimeShutdownStatus(t, ctx, reconciler.Client, repo)
}

func TestReconcileIgnoresStaleSyncJobPodWhenStartingNextGenerationSync(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	repo.Generation = 2
	staleSyncPod := syncJobPod(repo, "codegraph-api-service-sync-1-pod")
	reconciler := newTestReconciler(t, repo, staleSyncPod)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertExistsAndOwned(t, ctx, reconciler.Client, &batchv1.Job{}, repo, "codegraph-api-service-sync-2")
	assertRepositoryIndexingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
}

func TestReconcileWaitsForPreviousGenerationSyncJobBeforeStartingNextSync(t *testing.T) {
	ctx := context.Background()
	previousRepo := controllerRepository()
	previousJob := resources.BuildSyncJob(previousRepo, "ghcr.io/acme/codegraph:runtime")
	repo := controllerRepository()
	repo.Generation = 2
	reconciler := newTestReconciler(t, repo, previousJob)

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatalf("RequeueAfter = %s, want positive duration", result.RequeueAfter)
	}

	var job batchv1.Job
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service-sync-2"}, &job)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("sync job get error = %v, want NotFound", err)
	}
	assertRepositorySyncInProgressStatus(t, ctx, reconciler.Client, repo)
}

func TestReconcileStartsNextSyncAfterPreviousGenerationSyncJobCompletes(t *testing.T) {
	ctx := context.Background()
	previousRepo := controllerRepository()
	previousJob := resources.BuildSyncJob(previousRepo, "ghcr.io/acme/codegraph:runtime")
	previousJob.Status.Succeeded = 1
	repo := controllerRepository()
	repo.Generation = 2
	reconciler := newTestReconciler(t, repo, previousJob)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertExistsAndOwned(t, ctx, reconciler.Client, &batchv1.Job{}, repo, "codegraph-api-service-sync-2")
	assertRepositoryIndexingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
}

func TestReconcileCreatesNextGenerationSyncWhenRuntimePodsAreCurrent(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	repo.Generation = 2
	currentRuntimePod := runtimePod(repo, "codegraph-api-service-current", "2")
	reconciler := newTestReconciler(t, repo, currentRuntimePod)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertExistsAndOwned(t, ctx, reconciler.Client, &batchv1.Job{}, repo, "codegraph-api-service-sync-2")
	assertRepositoryIndexingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
}

func TestReconcileKeepsIndexingWhenCurrentSyncJobPodExists(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	repo.Generation = 2
	currentJob := resources.BuildSyncJob(repo, "ghcr.io/acme/codegraph:runtime")
	currentJobPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "codegraph-api-service-sync-2-pod",
			Namespace: repo.Namespace,
			Labels:    resources.LabelsFor(repo),
		},
	}
	reconciler := newTestReconciler(t, repo, currentJob, currentJobPod)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertRepositoryIndexingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
}

func TestReconcileAllowsDeploymentRefreshAfterCurrentGenerationJobSucceeded(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	repo.Generation = 2
	staleRuntime := resources.BuildDeployment(repo, "ghcr.io/acme/codegraph:runtime")
	staleRuntime.Spec.Template.Annotations[resources.RepositoryGenerationAnnotation] = "1"
	succeededJob := resources.BuildSyncJob(repo, "ghcr.io/acme/codegraph:runtime")
	succeededJob.Status.Succeeded = 1
	reconciler := newTestReconciler(t, repo, staleRuntime, succeededJob)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var deployment appsv1.Deployment
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, &deployment); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if got := deployment.Spec.Template.Annotations[resources.RepositoryGenerationAnnotation]; got != "2" {
		t.Fatalf("deployment repository generation annotation = %q, want 2", got)
	}
}

func TestReconcileRecordsRequestedRefAndLastSyncTimeFromSucceededJob(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	repo.Spec.Git.Ref = "release/2026-06"
	reconciler := newTestReconciler(t, repo)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}

	completion := metav1.NewTime(time.Date(2026, 6, 16, 12, 34, 56, 0, time.UTC))
	var job batchv1.Job
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service-sync-1"}, &job); err != nil {
		t.Fatalf("get job: %v", err)
	}
	job.Status.Succeeded = 1
	job.Status.CompletionTime = &completion
	if err := reconciler.Status().Update(ctx, &job); err != nil {
		t.Fatalf("update job status: %v", err)
	}

	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.ResolvedRef != "release/2026-06" {
		t.Fatalf("resolvedRef = %q", updated.Status.ResolvedRef)
	}
	if updated.Status.LastSyncTime == nil || !completion.Equal(updated.Status.LastSyncTime) {
		t.Fatalf("lastSyncTime = %#v, want %s", updated.Status.LastSyncTime, completion.Time)
	}
}

func TestReconcileMarksInvalidMCPPathDegradedWithoutCreatingResources(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	repo.Spec.MCP.Path = "/mcp/other-service"
	reconciler := newTestReconciler(t, repo)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	for _, object := range []client.Object{
		&corev1.PersistentVolumeClaim{},
		&batchv1.Job{},
		&corev1.Service{},
		&gatewayv1.HTTPRoute{},
	} {
		err := reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, object)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("%T get error = %v, want NotFound", object, err)
		}
	}

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhaseDegraded {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "InvalidMCPPath" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionFalse || indexed.Reason != "InvalidMCPPath" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

func TestReconcilePreservesIndexSucceededWhenRuntimeApplyFails(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	reconciler := newTestReconciler(t, repo)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}

	var job batchv1.Job
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service-sync-1"}, &job); err != nil {
		t.Fatalf("get job: %v", err)
	}
	job.Status.Succeeded = 1
	if err := reconciler.Status().Update(ctx, &job); err != nil {
		t.Fatalf("update job status: %v", err)
	}

	reconciler.Client = getDeploymentErrorClient{
		Client: reconciler.Client,
		err:    errors.New("deployment read failed"),
	}
	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err == nil {
		t.Fatalf("second Reconcile() error = nil, want deployment error")
	}

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := reconciler.Client.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhaseDegraded {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "DeploymentApplyFailed" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionTrue || indexed.Reason != "IndexSucceeded" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

func TestReconcileMarksDegradedWhenJobFails(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	reconciler := newTestReconciler(t, repo)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}

	var job batchv1.Job
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service-sync-1"}, &job); err != nil {
		t.Fatalf("get job: %v", err)
	}
	job.Status.Failed = 1
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:   batchv1.JobFailed,
			Status: corev1.ConditionTrue,
		},
	}
	if err := reconciler.Status().Update(ctx, &job); err != nil {
		t.Fatalf("update job status: %v", err)
	}

	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	var deployment appsv1.Deployment
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, &deployment)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("deployment get error = %v, want NotFound", err)
	}
	assertRepositoryDegradedIndexStatus(t, ctx, reconciler.Client, repo)
}

func TestReconcileKeepsRetryingJobIndexingUntilTerminalFailure(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	reconciler := newTestReconciler(t, repo)

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}

	var job batchv1.Job
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service-sync-1"}, &job); err != nil {
		t.Fatalf("get job: %v", err)
	}
	job.Status.Failed = 1
	if err := reconciler.Status().Update(ctx, &job); err != nil {
		t.Fatalf("update job status: %v", err)
	}

	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(repo)})
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	var deployment appsv1.Deployment
	err = reconciler.Get(ctx, types.NamespacedName{Namespace: repo.Namespace, Name: "codegraph-api-service"}, &deployment)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("deployment get error = %v, want NotFound", err)
	}
	assertRepositoryIndexingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
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

	assertRepositoryIndexingStatus(t, ctx, reconciler.Client, repo, "codegraph-api-service", "codegraph-api-service")
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

func TestMarkDegradedClearsStaleIndexedCondition(t *testing.T) {
	ctx := context.Background()
	repo := controllerRepository()
	reconciler := newTestReconciler(t, repo)

	if _, err := reconciler.markRuntimePending(ctx, repo); err != nil {
		t.Fatalf("markRuntimePending() error = %v", err)
	}

	wantErr := errors.New("pvc apply failed")
	if _, err := reconciler.markDegraded(ctx, repo, "PVCApplyFailed", wantErr); err != wantErr {
		t.Fatalf("markDegraded() error = %v, want %v", err, wantErr)
	}

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionFalse || indexed.Reason != "PVCApplyFailed" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

type getDeploymentErrorClient struct {
	client.Client
	err error
}

func (c getDeploymentErrorClient) Get(ctx context.Context, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
	if _, ok := object.(*appsv1.Deployment); ok {
		return c.err
	}
	return c.Client.Get(ctx, key, object, opts...)
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
		WithStatusSubresource(repo, &batchv1.Job{}, &appsv1.Deployment{}).
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

func runtimePod(repo *codegraphv1alpha1.CodeGraphRepository, name string, generation string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   repo.Namespace,
			Labels:      resources.RuntimeSelectorFor(repo),
			Annotations: map[string]string{resources.RepositoryGenerationAnnotation: generation},
		},
	}
}

func syncJobPod(repo *codegraphv1alpha1.CodeGraphRepository, name string) *corev1.Pod {
	labels := resources.LabelsFor(repo)
	labels[resources.WorkloadLabel] = resources.WorkloadSync
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: repo.Namespace,
			Labels:    labels,
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

func assertRepositoryIndexingStatus(t *testing.T, ctx context.Context, c client.Client, repo *codegraphv1alpha1.CodeGraphRepository, serviceName string, routeName string) {
	t.Helper()

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := c.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.ObservedGeneration != repo.Generation {
		t.Fatalf("observedGeneration = %d", updated.Status.ObservedGeneration)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhaseIndexing {
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
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "IndexRunning" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionFalse || indexed.Reason != "IndexRunning" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

func assertRepositoryRuntimePendingStatus(t *testing.T, ctx context.Context, c client.Client, repo *codegraphv1alpha1.CodeGraphRepository, serviceName string, routeName string) {
	t.Helper()

	updated := assertRepositoryStatusNames(t, ctx, c, repo, serviceName, routeName)
	if updated.Status.Phase != codegraphv1alpha1.PhasePending {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "RuntimeUnavailable" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionTrue || indexed.Reason != "IndexSucceeded" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

func assertRepositoryReadyStatus(t *testing.T, ctx context.Context, c client.Client, repo *codegraphv1alpha1.CodeGraphRepository, serviceName string, routeName string) {
	t.Helper()

	updated := assertRepositoryStatusNames(t, ctx, c, repo, serviceName, routeName)
	if updated.Status.Phase != codegraphv1alpha1.PhaseReady {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.Reason != "RuntimeAvailable" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionTrue || indexed.Reason != "IndexSucceeded" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

func assertRepositoryDegradedIndexStatus(t *testing.T, ctx context.Context, c client.Client, repo *codegraphv1alpha1.CodeGraphRepository) {
	t.Helper()

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := c.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhaseDegraded {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "IndexFailed" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionFalse || indexed.Reason != "IndexFailed" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

func assertRepositoryRuntimeShutdownStatus(t *testing.T, ctx context.Context, c client.Client, repo *codegraphv1alpha1.CodeGraphRepository) {
	t.Helper()

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := c.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhasePending {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "RuntimeShutdown" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionFalse || indexed.Reason != "RuntimeShutdown" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

func assertRepositorySyncInProgressStatus(t *testing.T, ctx context.Context, c client.Client, repo *codegraphv1alpha1.CodeGraphRepository) {
	t.Helper()

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := c.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhasePending {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "SyncInProgress" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionFalse || indexed.Reason != "SyncInProgress" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

func assertRepositoryRuntimeImageMissingStatus(t *testing.T, ctx context.Context, c client.Client, repo *codegraphv1alpha1.CodeGraphRepository) {
	t.Helper()

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := c.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.ObservedGeneration != repo.Generation {
		t.Fatalf("observedGeneration = %d", updated.Status.ObservedGeneration)
	}
	if updated.Status.Phase != codegraphv1alpha1.PhaseDegraded {
		t.Fatalf("phase = %q", updated.Status.Phase)
	}
	ready := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "RuntimeImageMissing" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	indexed := apiMeta.FindStatusCondition(updated.Status.Conditions, codegraphv1alpha1.ConditionIndexed)
	if indexed == nil || indexed.Status != metav1.ConditionFalse || indexed.Reason != "RuntimeImageMissing" {
		t.Fatalf("Indexed condition = %#v", indexed)
	}
}

func assertRepositoryStatusNames(t *testing.T, ctx context.Context, c client.Client, repo *codegraphv1alpha1.CodeGraphRepository, serviceName string, routeName string) codegraphv1alpha1.CodeGraphRepository {
	t.Helper()

	var updated codegraphv1alpha1.CodeGraphRepository
	if err := c.Get(ctx, client.ObjectKeyFromObject(repo), &updated); err != nil {
		t.Fatalf("get updated repo: %v", err)
	}
	if updated.Status.ObservedGeneration != repo.Generation {
		t.Fatalf("observedGeneration = %d", updated.Status.ObservedGeneration)
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
	return updated
}
