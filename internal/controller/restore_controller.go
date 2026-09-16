package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
	"github.com/mahdidarabi/KArkive/internal/resources"
)

var requiredRestoreSecretKeys = []string{
	"s3_access_key",
	"s3_secret_key",
	"gpg_passphrase",
}

// RestoreReconciler reconciles a Restore object into ConfigMap + optional PVC + CronJob.
type RestoreReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Config   config.Config
}

// +kubebuilder:rbac:groups=karkive.io,resources=restores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karkive.io,resources=restores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karkive.io,resources=restores/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *RestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	restore := &karkivev1alpha1.Restore{}
	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !restore.DeletionTimestamp.IsZero() {
		logger.Info("Restore is deleting; not recreating owned resources")
		return ctrl.Result{}, nil
	}

	if !resources.EngineImplemented(restore.Spec.Engine) {
		engine := resources.EffectiveEngine(restore.Spec.Engine)
		msg := fmt.Sprintf("engine %q is not implemented", engine)
		logger.Info(msg)
		return ctrl.Result{}, r.setStatus(ctx, restore, karkivev1alpha1.RestorePhaseUnsupported, metav1.ConditionFalse, "UnsupportedEngine", msg, corev1.EventTypeWarning, nil, "")
	}

	if err := karkivev1alpha1.ValidateRestoreSpec(restore.Spec); err != nil {
		return ctrl.Result{}, r.setStatus(ctx, restore, karkivev1alpha1.RestorePhaseError, metav1.ConditionFalse, "InvalidSpec", err.Error(), corev1.EventTypeWarning, nil, "")
	}

	if err := r.ensureJobSecret(ctx, restore); err != nil {
		if apierrors.IsNotFound(err) {
			msg := fmt.Sprintf("secret %q not found", restore.Spec.SecretRef.Name)
			if statusErr := r.setStatus(ctx, restore, karkivev1alpha1.RestorePhasePending, metav1.ConditionFalse, "SecretNotFound", msg, corev1.EventTypeWarning, nil, ""); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{RequeueAfter: secretRequeue}, nil
		}
		return ctrl.Result{}, r.setStatus(ctx, restore, karkivev1alpha1.RestorePhaseError, metav1.ConditionFalse, "SecretInvalid", err.Error(), corev1.EventTypeWarning, nil, "")
	}

	if err := r.ensureTargetSecret(ctx, restore); err != nil {
		if apierrors.IsNotFound(err) {
			name, _, _ := resources.RestoreTargetSecret(restore)
			msg := fmt.Sprintf("target secret %q not found", name)
			if statusErr := r.setStatus(ctx, restore, karkivev1alpha1.RestorePhasePending, metav1.ConditionFalse, "TargetSecretNotFound", msg, corev1.EventTypeWarning, nil, ""); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{RequeueAfter: secretRequeue}, nil
		}
		return ctrl.Result{}, r.setStatus(ctx, restore, karkivev1alpha1.RestorePhaseError, metav1.ConditionFalse, "TargetSecretInvalid", err.Error(), corev1.EventTypeWarning, nil, "")
	}

	owned := resources.RestoreOwnedName(restore)
	holderName := resources.VolumeHolderName(owned)
	needHolder := resources.NeedsVolumeHolder(restore.Spec.Runtime)
	cron, err := ensureOwned(ctx, r.Client, r.Scheme, ownedResources{
		Owner:        restore,
		Name:         owned,
		Persistence:  restore.Spec.Persistence,
		Labels:       resources.RestoreLabels(restore),
		VolumeHolder: needHolder,
		HolderName:   holderName,
		MutateConfigMap: func(cm *corev1.ConfigMap) error {
			return resources.MutateRestoreConfigMap(cm, restore, r.Config)
		},
		MutateCronJob: func(cj *batchv1.CronJob) {
			resources.MutateRestoreCronJob(cj, restore, r.Config)
		},
		MutateVolumeHolder: func(deploy *appsv1.Deployment) {
			resources.MutateVolumeHolderDeployment(deploy, resources.VolumeHolderSpec{
				Name:      holderName,
				Labels:    resources.WithRuntimeRole(resources.RestoreLabels(restore), resources.RuntimeRoleVolumeHolder),
				Selector:  resources.VolumeHolderMatchLabels(resources.KindRestore, restore.Name),
				ClaimName: owned,
				MountPath: resources.RestoreVolumeMountPath(restore),
				Images:    restore.Spec.Images,
			}, r.Config)
		},
	})
	if err != nil {
		return ctrl.Result{}, r.fail(ctx, restore, ownedReason(err), err)
	}
	if err := deleteLegacyOwned(ctx, r.Client, restore, owned); err != nil {
		return ctrl.Result{}, r.fail(ctx, restore, "LegacyCleanupError", err)
	}

	statusHolder := ""
	if needHolder {
		statusHolder = holderName
		deploy := &appsv1.Deployment{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: restore.Namespace, Name: holderName}, deploy); err != nil {
			return ctrl.Result{}, r.fail(ctx, restore, "VolumeHolderError", err)
		}
		if !volumeHolderAvailable(deploy) {
			msg := fmt.Sprintf("waiting for volume holder Deployment %s", holderName)
			if statusErr := r.setStatus(ctx, restore, karkivev1alpha1.RestorePhasePending, metav1.ConditionFalse, "VolumeHolderNotReady", msg, corev1.EventTypeNormal, cron, statusHolder); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{RequeueAfter: holderRequeue}, nil
		}
	}

	msg := fmt.Sprintf("CronJob %s is synced", cron.Name)
	return ctrl.Result{}, r.setStatus(ctx, restore, karkivev1alpha1.RestorePhaseReady, metav1.ConditionTrue, "Synced", msg, corev1.EventTypeNormal, cron, statusHolder)
}

func (r *RestoreReconciler) fail(ctx context.Context, restore *karkivev1alpha1.Restore, reason string, err error) error {
	if statusErr := r.setStatus(ctx, restore, karkivev1alpha1.RestorePhaseError, metav1.ConditionFalse, reason, err.Error(), corev1.EventTypeWarning, nil, ""); statusErr != nil {
		return statusErr
	}
	return err
}

func (r *RestoreReconciler) ensureJobSecret(ctx context.Context, restore *karkivev1alpha1.Restore) error {
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: restore.Namespace, Name: restore.Spec.SecretRef.Name}
	if err := r.Get(ctx, key, secret); err != nil {
		return err
	}
	for _, k := range requiredRestoreSecretKeys {
		if _, ok := secret.Data[k]; !ok {
			return fmt.Errorf("secret %q is missing key %q", secret.Name, k)
		}
	}
	return nil
}

func (r *RestoreReconciler) ensureTargetSecret(ctx context.Context, restore *karkivev1alpha1.Restore) error {
	name, userKey, passKey := resources.RestoreTargetSecret(restore)
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: restore.Namespace, Name: name}
	if err := r.Get(ctx, key, secret); err != nil {
		return err
	}
	for _, k := range []string{userKey, passKey} {
		if _, ok := secret.Data[k]; !ok {
			return fmt.Errorf("target secret %q is missing key %q", secret.Name, k)
		}
	}
	return nil
}

func (r *RestoreReconciler) setStatus(
	ctx context.Context,
	restore *karkivev1alpha1.Restore,
	phase string,
	ready metav1.ConditionStatus,
	reason, message, eventType string,
	cron *batchv1.CronJob,
	holderName string,
) error {
	specChanged := generationChanged(restore.Generation, restore.Status.ObservedGeneration, restore.Status.Phase)
	phaseChanged := restore.Status.Phase != phase
	newLastJob := lastJobStatus(ctx, r.Client, restore.Namespace, resources.LabelRestoreName, restore.Name, resources.KindRestore)
	lastJobChanged := !lastJobEqual(restore.Status.LastJob, newLastJob)

	cronChanged := false
	if cron != nil {
		cronChanged = restore.Status.CronJobName != cron.Name ||
			!metaTimeEqual(restore.Status.LastScheduleTime, cron.Status.LastScheduleTime) ||
			!metaTimeEqual(restore.Status.LastSuccessfulTime, cron.Status.LastSuccessfulTime)
		restore.Status.CronJobName = cron.Name
		restore.Status.LastScheduleTime = cron.Status.LastScheduleTime
		restore.Status.LastSuccessfulTime = cron.Status.LastSuccessfulTime
	}
	holderChanged := restore.Status.VolumeHolderName != holderName
	restore.Status.VolumeHolderName = holderName

	readyChanged := meta.SetStatusCondition(&restore.Status.Conditions, metav1.Condition{
		Type:               karkivev1alpha1.ConditionReady,
		Status:             ready,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: restore.Generation,
	})
	succeededChanged := setJobSucceededCondition(&restore.Status.Conditions, karkivev1alpha1.ConditionRestoreSucceeded, restore.Generation, newLastJob)
	condChanged := readyChanged || succeededChanged
	restore.Status.LastJob = newLastJob
	restore.Status.Phase = phase
	restore.Status.ObservedGeneration = restore.Generation

	if r.Recorder != nil && shouldRecordEvent(eventType, specChanged, phaseChanged, readyChanged) {
		r.Recorder.Event(restore, eventType, reason, message)
	}
	if !statusNeedsPatch(specChanged, phaseChanged, condChanged, lastJobChanged, cronChanged || holderChanged) {
		return nil
	}
	return r.Status().Update(ctx, restore)
}

func (r *RestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karkivev1alpha1.Restore{}).
		Owns(&batchv1.CronJob{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&appsv1.Deployment{}).
		Watches(&batchv1.Job{}, handler.EnqueueRequestsFromMapFunc(mapJobToOwner(resources.KindRestore, resources.LabelRestoreName))).
		Complete(r)
}
