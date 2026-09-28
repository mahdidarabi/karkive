package resources

import (
	corev1 "k8s.io/api/core/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
)

// MutatePVC writes the PVC spec. Call only when creating; spec is mostly immutable.
func MutatePVC(pvc *corev1.PersistentVolumeClaim, persistence *karkivev1alpha1.PersistenceSpec, labels map[string]string) {
	accessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	if persistence != nil && len(persistence.AccessModes) > 0 {
		accessModes = persistence.AccessModes
	}

	pvc.Labels = labels
	if persistence != nil && len(persistence.Annotations) > 0 {
		pvc.Annotations = persistence.Annotations
	}

	spec := corev1.PersistentVolumeClaimSpec{
		AccessModes: accessModes,
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: persistenceSize(persistence),
			},
		},
	}
	if persistence != nil && persistence.StorageClassName != "" {
		sc := persistence.StorageClassName
		spec.StorageClassName = &sc
	}
	pvc.Spec = spec
}

// ExpandPVC raises an existing PVC's storage request to persistence.size.
// Kubernetes rejects shrinking a claim, so a smaller size is ignored. Growing
// needs a StorageClass with allowVolumeExpansion; the API server rejects it otherwise.
func ExpandPVC(pvc *corev1.PersistentVolumeClaim, persistence *karkivev1alpha1.PersistenceSpec) {
	want := persistenceSize(persistence)
	if cur, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok && want.Cmp(cur) <= 0 {
		return
	}
	if pvc.Spec.Resources.Requests == nil {
		pvc.Spec.Resources.Requests = corev1.ResourceList{}
	}
	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = want
}
