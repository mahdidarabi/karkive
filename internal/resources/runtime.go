package resources

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
)

const (
	RuntimeRoleVolumeHolder = "volume-holder"
	volumeHolderNameSuffix  = "-holder"
	kubernetesHostnameLabel = "kubernetes.io/hostname"
	dns1035LabelMax         = 63
)

// NeedsVolumeHolder is true when the CR asked for CronJobWithVolumeHolder
// (validation already requires persistence).
func NeedsVolumeHolder(runtime *karkivev1alpha1.RuntimeSpec) bool {
	return runtime.EffectiveMode() == karkivev1alpha1.RuntimeModeCronJobWithVolumeHolder
}

// VolumeHolderName is the pause Deployment name for an owned ConfigMap/PVC/CronJob name.
func VolumeHolderName(owned string) string {
	if len(owned)+len(volumeHolderNameSuffix) <= dns1035LabelMax {
		return owned + volumeHolderNameSuffix
	}
	keep := dns1035LabelMax - len(volumeHolderNameSuffix)
	return strings.TrimRight(owned[:keep], "-") + volumeHolderNameSuffix
}

// VolumeHolderMatchLabels selects only the pause pod (not CronJob Job pods).
func VolumeHolderMatchLabels(kind, crName string) map[string]string {
	labels := map[string]string{
		LabelKind:        kind,
		LabelRuntimeRole: RuntimeRoleVolumeHolder,
	}
	switch kind {
	case KindRestore:
		labels[LabelRestoreName] = crName
	default:
		labels[LabelBackupName] = crName
	}
	return labels
}

// WithRuntimeRole copies labels and sets karkive.io/runtime-role.
func WithRuntimeRole(labels map[string]string, role string) map[string]string {
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	out[LabelRuntimeRole] = role
	return out
}

// VolumeHolderAffinity forces Job pods onto the node's holder (RWO attach is per node).
func VolumeHolderAffinity(matchLabels map[string]string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAffinity: &corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: matchLabels},
				TopologyKey:   kubernetesHostnameLabel,
			}},
		},
	}
}

func applyVolumeHolderAffinity(pod *corev1.PodSpec, matchLabels map[string]string) {
	if matchLabels == nil {
		return
	}
	pod.Affinity = VolumeHolderAffinity(matchLabels)
}
