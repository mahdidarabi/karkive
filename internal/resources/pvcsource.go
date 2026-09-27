package resources

import (
	"path"

	corev1 "k8s.io/api/core/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
	"github.com/mahdidarabi/KArkive/internal/pipeline"
)

const volumeSource = "source"

// IsPVCBackup is true for engine pvc (tar a claim instead of dumping a database).
func IsPVCBackup(backup *karkivev1alpha1.Backup) bool {
	return EffectiveEngine(backup.Spec.Engine) == karkivev1alpha1.EnginePVC
}

// pvcSource is never nil so callers can read fields; validation requires spec.pvc.
func pvcSource(backup *karkivev1alpha1.Backup) *karkivev1alpha1.PVCSourceSpec {
	if backup.Spec.PVC == nil {
		return &karkivev1alpha1.PVCSourceSpec{}
	}
	return backup.Spec.PVC
}

// pvcSourcePath is spec.pvc.path cleaned; empty means the claim root.
func pvcSourcePath(src *karkivev1alpha1.PVCSourceSpec) string {
	if src.Path == "" {
		return ""
	}
	p := path.Clean(src.Path)
	if p == "." {
		return ""
	}
	return p
}

// PVCConsumerAffinityTerms colocates pods with the source claim's consumers
// (RWO attaches to one node). Empty unless spec.pvc.consumerSelector is set.
func PVCConsumerAffinityTerms(backup *karkivev1alpha1.Backup) []corev1.PodAffinityTerm {
	if !IsPVCBackup(backup) || backup.Spec.PVC == nil || backup.Spec.PVC.ConsumerSelector == nil {
		return nil
	}
	return []corev1.PodAffinityTerm{{
		LabelSelector: backup.Spec.PVC.ConsumerSelector.DeepCopy(),
		TopologyKey:   kubernetesHostnameLabel,
	}}
}

func pvcDumpContainer(
	backup *karkivev1alpha1.Backup,
	cfg config.Config,
	res corev1.ResourceRequirements,
	cmName string,
) corev1.Container {
	img, pull := tarImage(backup.Spec.Images, cfg)
	c := newScriptContainer(scriptOpts{
		Name: "pvcdump", Image: img, Pull: pull,
		Script:    pipeline.MustBackupScript("pvcdump.sh"),
		ConfigMap: cmName, Resources: res,
		Security: PVCDumpSecurityContext(backup.Spec.PVC), TmpSubPath: "tmp-dir",
	})
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
		Name:      volumeSource,
		MountPath: config.DefaultPVCSourceDir,
		ReadOnly:  true,
	})
	return c
}

// pvcSourceVolume mounts the claim read-only. ReadOnly on the volume source
// (not only the mount) also keeps kubelet from applying the pod fsGroup, which
// would otherwise chown the app's files.
func pvcSourceVolume(backup *karkivev1alpha1.Backup) corev1.Volume {
	return corev1.Volume{
		Name: volumeSource,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcSource(backup).ClaimName,
				ReadOnly:  true,
			},
		},
	}
}
