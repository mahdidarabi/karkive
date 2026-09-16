package resources

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
	"github.com/mahdidarabi/KArkive/internal/ptr"
)

const volumeHolderContainer = "volume-holder"

// volumeHolderCommand logs once so kubectl logs shows the pod is holding the PVC,
// then sleeps. Recreate + podAffinity keep CronJob pods on this node.
const volumeHolderCommand = `echo "[volume-holder $(date '+%Y-%m-%dT%H:%M:%S%z')] INFO keeping PVC attached on this node" >&2
while true; do sleep 3600; done`

// VolumeHolderSpec is the pause Deployment that keeps a PVC attached.
type VolumeHolderSpec struct {
	Name      string
	Labels    map[string]string
	Selector  map[string]string
	ClaimName string
	MountPath string
	Images    *karkivev1alpha1.ImageSet
}

// MutateVolumeHolderDeployment writes a 1-replica Recreate pause pod on the PVC.
func MutateVolumeHolderDeployment(deploy *appsv1.Deployment, spec VolumeHolderSpec, cfg config.Config) {
	image, pull := busyBoxImage(spec.Images, cfg)
	labels := spec.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	selector := spec.Selector
	if selector == nil {
		selector = map[string]string{}
	}
	mount := spec.MountPath
	if mount == "" {
		mount = mountDataDir
	}

	deploy.Labels = labels
	deploy.Spec = appsv1.DeploymentSpec{
		Replicas: ptr.To(int32(1)),
		Selector: &metav1.LabelSelector{MatchLabels: selector},
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken:  ptr.To(false),
				RestartPolicy:                 corev1.RestartPolicyAlways,
				SecurityContext:               PodSecurityContext(),
				TerminationGracePeriodSeconds: ptr.To(int64(5)),
				Containers: []corev1.Container{{
					Name:            volumeHolderContainer,
					Image:           image,
					ImagePullPolicy: pull,
					Command:         []string{"/bin/sh", "-c", volumeHolderCommand},
					Resources:       DefaultVolumeHolderResources(),
					SecurityContext: ToolsSecurityContext(),
					VolumeMounts: []corev1.VolumeMount{{
						Name:      volumeDataDir,
						MountPath: mount,
					}},
				}},
				Volumes: []corev1.Volume{{
					Name: volumeDataDir,
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: spec.ClaimName,
						},
					},
				}},
			},
		},
	}
}

func DefaultVolumeHolderResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("1m"),
			corev1.ResourceMemory:           resource.MustParse("8Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("1Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("10m"),
			corev1.ResourceMemory:           resource.MustParse("16Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
		},
	}
}
