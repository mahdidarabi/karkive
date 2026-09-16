package resources

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
)

func TestVolumeHolderName(t *testing.T) {
	got := VolumeHolderName("karkive-backup-app-postgres")
	if got != "karkive-backup-app-postgres-holder" {
		t.Errorf("VolumeHolderName=%q", got)
	}
	long := strings.Repeat("a", 60)
	name := VolumeHolderName(long)
	if len(name) > 63 {
		t.Errorf("len=%d", len(name))
	}
	if !strings.HasSuffix(name, "-holder") {
		t.Errorf("name=%q", name)
	}
}

func TestNeedsVolumeHolder(t *testing.T) {
	if NeedsVolumeHolder(nil) {
		t.Fatal("nil runtime")
	}
	if NeedsVolumeHolder(&karkivev1alpha1.RuntimeSpec{Mode: karkivev1alpha1.RuntimeModeCronJob}) {
		t.Fatal("CronJob")
	}
	if !NeedsVolumeHolder(&karkivev1alpha1.RuntimeSpec{Mode: karkivev1alpha1.RuntimeModeCronJobWithVolumeHolder}) {
		t.Fatal("holder")
	}
}

func TestMutateBackupCronJob_VolumeHolderAffinity(t *testing.T) {
	backup := testBackup()
	backup.Spec.Runtime = &karkivev1alpha1.RuntimeSpec{Mode: karkivev1alpha1.RuntimeModeCronJobWithVolumeHolder}
	cj := &batchv1.CronJob{}
	MutateBackupCronJob(cj, backup, config.Config{})
	aff := cj.Spec.JobTemplate.Spec.Template.Spec.Affinity
	if aff == nil || aff.PodAffinity == nil {
		t.Fatal("expected podAffinity")
	}
	terms := aff.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if len(terms) != 1 || terms[0].TopologyKey != "kubernetes.io/hostname" {
		t.Fatalf("terms=%#v", terms)
	}
	want := VolumeHolderMatchLabels(KindBackup, backup.Name)
	for k, v := range want {
		if terms[0].LabelSelector.MatchLabels[k] != v {
			t.Errorf("match[%s]=%q, want %q", k, terms[0].LabelSelector.MatchLabels[k], v)
		}
	}
}

func TestMutateVolumeHolderDeployment(t *testing.T) {
	deploy := &appsv1.Deployment{}
	MutateVolumeHolderDeployment(deploy, VolumeHolderSpec{
		Name:      "karkive-backup-app-postgres-holder",
		Labels:    WithRuntimeRole(map[string]string{LabelBackupName: "app-postgres"}, RuntimeRoleVolumeHolder),
		Selector:  VolumeHolderMatchLabels(KindBackup, "app-postgres"),
		ClaimName: "karkive-backup-app-postgres",
		MountPath: "/backup/data",
	}, config.Config{})
	if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 1 {
		t.Fatalf("replicas=%v", deploy.Spec.Replicas)
	}
	if deploy.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("strategy=%q", deploy.Spec.Strategy.Type)
	}
	pod := deploy.Spec.Template.Spec
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("expected automountServiceAccountToken false")
	}
	if len(pod.Containers) != 1 || pod.Containers[0].Name != "volume-holder" {
		t.Fatalf("containers=%v", pod.Containers)
	}
	cmd := strings.Join(pod.Containers[0].Command, " ")
	if !strings.Contains(cmd, "INFO keeping PVC attached") {
		t.Errorf("holder command missing info log: %q", cmd)
	}
	if pod.Volumes[0].PersistentVolumeClaim == nil || pod.Volumes[0].PersistentVolumeClaim.ClaimName != "karkive-backup-app-postgres" {
		t.Fatalf("volume=%#v", pod.Volumes[0])
	}
	if pod.RestartPolicy != corev1.RestartPolicyAlways {
		t.Errorf("restartPolicy=%q", pod.RestartPolicy)
	}
}

func TestMutateRestoreCronJob_NoAffinityByDefault(t *testing.T) {
	cj := &batchv1.CronJob{}
	MutateRestoreCronJob(cj, testRestore(), config.Config{})
	if cj.Spec.JobTemplate.Spec.Template.Spec.Affinity != nil {
		t.Fatal("default restore must not set affinity")
	}
}
