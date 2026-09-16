package controller

import (
	"context"
	"errors"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/resources"
)

// ownedResources is the ConfigMap + optional PVC + CronJob (+ optional holder) for one Backup or Restore.
type ownedResources struct {
	Owner              client.Object
	Name               string
	Persistence        *karkivev1alpha1.PersistenceSpec
	Labels             map[string]string
	VolumeHolder       bool
	HolderName         string
	MutateConfigMap    func(*corev1.ConfigMap) error
	MutateCronJob      func(*batchv1.CronJob)
	MutateVolumeHolder func(*appsv1.Deployment)
}

type ownedApplyError struct {
	Reason string
	Err    error
}

func (e *ownedApplyError) Error() string { return e.Err.Error() }
func (e *ownedApplyError) Unwrap() error { return e.Err }

func ownedReason(err error) string {
	var ae *ownedApplyError
	if errors.As(err, &ae) {
		return ae.Reason
	}
	return "OwnedResourceError"
}

func applyOwnedError(reason string, err error) error {
	if err == nil {
		return nil
	}
	return &ownedApplyError{Reason: reason, Err: err}
}

// ensureOwned creates or updates the ConfigMap, optional PVC, optional volume
// holder Deployment, and CronJob for a CR.
func ensureOwned(ctx context.Context, c client.Client, scheme *runtime.Scheme, spec ownedResources) (*batchv1.CronJob, error) {
	ns := spec.Owner.GetNamespace()
	setOwner := func(obj client.Object) error {
		return controllerutil.SetControllerReference(spec.Owner, obj, scheme, controllerutil.WithBlockOwnerDeletion(false))
	}

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, cm, func() error {
		if err := spec.MutateConfigMap(cm); err != nil {
			return err
		}
		return setOwner(cm)
	}); err != nil {
		return nil, applyOwnedError("ConfigMapError", err)
	}

	if resources.PersistenceEnabled(spec.Persistence) {
		pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns}}
		if _, err := controllerutil.CreateOrUpdate(ctx, c, pvc, func() error {
			if !pvc.CreationTimestamp.IsZero() {
				pvc.Labels = spec.Labels
				return setOwner(pvc)
			}
			resources.MutatePVC(pvc, spec.Persistence, spec.Labels)
			return setOwner(pvc)
		}); err != nil {
			return nil, applyOwnedError("PVCError", err)
		}
	}

	if spec.VolumeHolder {
		deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: spec.HolderName, Namespace: ns}}
		if _, err := controllerutil.CreateOrUpdate(ctx, c, deploy, func() error {
			spec.MutateVolumeHolder(deploy)
			return setOwner(deploy)
		}); err != nil {
			return nil, applyOwnedError("VolumeHolderError", err)
		}
	} else if spec.HolderName != "" {
		if _, err := deleteIfController(ctx, c, spec.Owner, &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: spec.HolderName, Namespace: ns},
		}); err != nil {
			return nil, applyOwnedError("VolumeHolderCleanupError", err)
		}
	}

	cj := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, cj, func() error {
		spec.MutateCronJob(cj)
		return setOwner(cj)
	}); err != nil {
		return nil, applyOwnedError("CronJobError", err)
	}
	return cj, nil
}

// deleteLegacyOwned removes the pre-0.0.4 ConfigMap and CronJob named
// karkive-<cr-name> when this CR still owns them. The PVC is left in place
// because Kubernetes cannot rename it; retained dumps must be copied by the
// operator if still needed.
func deleteLegacyOwned(ctx context.Context, c client.Client, owner client.Object, currentName string) error {
	legacy := resources.LegacyOwnedName(owner.GetName())
	if legacy == currentName {
		return nil
	}
	ns := owner.GetNamespace()
	logger := log.FromContext(ctx)

	cmDeleted, err := deleteIfController(ctx, c, owner, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: legacy, Namespace: ns},
	})
	if err != nil {
		return err
	}
	cjDeleted, err := deleteIfController(ctx, c, owner, &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: legacy, Namespace: ns},
	})
	if err != nil {
		return err
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: legacy}, pvc); err == nil && metav1.IsControlledBy(pvc, owner) {
		logger.Info("legacy PVC kept (Kubernetes cannot rename it); copy retained dumps then delete", "name", legacy)
	}

	if cmDeleted || cjDeleted {
		logger.Info("deleted legacy owned ConfigMap/CronJob", "legacy", legacy, "current", currentName)
	}
	return nil
}

func deleteIfController(ctx context.Context, c client.Client, owner client.Object, obj client.Object) (bool, error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(obj, owner) {
		return false, nil
	}
	if err := c.Delete(ctx, obj); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return true, nil
}

func volumeHolderAvailable(deploy *appsv1.Deployment) bool {
	return deploy.Status.AvailableReplicas >= 1
}
