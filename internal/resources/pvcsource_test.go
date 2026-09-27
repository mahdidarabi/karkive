package resources

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
	"github.com/mahdidarabi/KArkive/internal/ptr"
)

func testPVCBackup() *karkivev1alpha1.Backup {
	return &karkivev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "nextcloud-data", Namespace: "nextcloud"},
		Spec: karkivev1alpha1.BackupSpec{
			Engine:   karkivev1alpha1.EnginePVC,
			Schedule: "0 3 * * *",
			PVC: &karkivev1alpha1.PVCSourceSpec{
				ClaimName: "nextcloud-data",
				Path:      "data/",
				Excludes:  []string{"cache", "./tmp"},
			},
			S3: karkivev1alpha1.S3Spec{
				Endpoint: "https://s3.example.com",
				Bucket:   "backups",
				Path:     "nextcloud/pvcdump",
			},
			SecretRef: corev1.LocalObjectReference{Name: "backup-nextcloud-data"},
		},
	}
}

func TestMutateBackupConfigMap_PVC(t *testing.T) {
	cm := &corev1.ConfigMap{}
	if err := MutateBackupConfigMap(cm, testPVCBackup(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ENGINE":         "pvc",
		"DUMP_PREFIX":    "pvcdump",
		"PVC_CLAIM_NAME": "nextcloud-data",
		"PVC_SOURCE_DIR": config.DefaultPVCSourceDir,
		"PVC_PATH":       "data",
		"PVC_EXCLUDES":   "cache\n./tmp",
		"S3_PATH":        "nextcloud/pvcdump",
	}
	for k, v := range want {
		if cm.Data[k] != v {
			t.Errorf("data[%s]=%q, want %q", k, cm.Data[k], v)
		}
	}
	for _, k := range []string{"PGHOST", "PGDATABASE", "MYSQL_HOST", "REDIS_HOST"} {
		if _, ok := cm.Data[k]; ok {
			t.Errorf("pvc ConfigMap should not set %s", k)
		}
	}

	root := testPVCBackup()
	root.Spec.PVC.Path = "./"
	if err := MutateBackupConfigMap(cm, root, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if cm.Data["PVC_PATH"] != "" {
		t.Errorf("PVC_PATH=%q, want empty for the claim root", cm.Data["PVC_PATH"])
	}
}

func TestMutateBackupCronJob_PVCStages(t *testing.T) {
	cj := &batchv1.CronJob{}
	MutateBackupCronJob(cj, testPVCBackup(), config.Config{})

	got := containerNames(cj)
	want := []string{"cleanup", "pvcdump", "compress", "encrypt", "s3-sync"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("containers=%v, want %v", got, want)
	}
	pod := cj.Spec.JobTemplate.Spec.Template.Spec
	dump := pod.Containers[1]
	if dump.Image != config.DefaultTarImage {
		t.Errorf("pvcdump image=%s", dump.Image)
	}
	if len(dump.Env) != 0 {
		t.Errorf("pvcdump needs no secret env, got %v", dump.Env)
	}
	if len(dump.Command) < 3 || !strings.Contains(dump.Command[2], "--numeric-owner") {
		t.Error("pvcdump script should run GNU tar with --numeric-owner")
	}

	var mount *corev1.VolumeMount
	for i := range dump.VolumeMounts {
		if dump.VolumeMounts[i].Name == volumeSource {
			mount = &dump.VolumeMounts[i]
		}
	}
	if mount == nil || !mount.ReadOnly || mount.MountPath != config.DefaultPVCSourceDir {
		t.Fatalf("source mount=%#v", mount)
	}
	for _, c := range pod.Containers {
		if c.Name == "pvcdump" {
			continue
		}
		for _, m := range c.VolumeMounts {
			if m.Name == volumeSource {
				t.Errorf("%s must not mount the source claim", c.Name)
			}
		}
	}

	var source *corev1.Volume
	for i := range pod.Volumes {
		if pod.Volumes[i].Name == volumeSource {
			source = &pod.Volumes[i]
		}
	}
	if source == nil || source.PersistentVolumeClaim == nil {
		t.Fatalf("source volume=%#v", source)
	}
	if source.PersistentVolumeClaim.ClaimName != "nextcloud-data" || !source.PersistentVolumeClaim.ReadOnly {
		t.Errorf("source claim=%#v (want nextcloud-data, readOnly)", source.PersistentVolumeClaim)
	}
	if pod.Affinity != nil {
		t.Error("no consumerSelector and default runtime: no affinity expected")
	}
}

func TestMutateBackupCronJob_DatabaseEnginesHaveNoSourceVolume(t *testing.T) {
	for _, b := range []*karkivev1alpha1.Backup{testBackup(), testMariaBackup(), testRedisBackup()} {
		cj := &batchv1.CronJob{}
		MutateBackupCronJob(cj, b, config.Config{})
		for _, v := range cj.Spec.JobTemplate.Spec.Template.Spec.Volumes {
			if v.Name == volumeSource {
				t.Errorf("%s: unexpected source volume", b.Spec.Engine)
			}
		}
	}
}

func TestPVCDumpSecurityContext(t *testing.T) {
	root := PVCDumpSecurityContext(nil)
	if root.RunAsUser == nil || *root.RunAsUser != 0 || root.RunAsNonRoot == nil || *root.RunAsNonRoot {
		t.Fatalf("default should be root: %#v", root)
	}
	if got := root.Capabilities.Add; len(got) != 1 || got[0] != "DAC_OVERRIDE" {
		t.Errorf("caps add=%v, want [DAC_OVERRIDE]", got)
	}
	if got := root.Capabilities.Drop; len(got) != 1 || got[0] != "ALL" {
		t.Errorf("caps drop=%v, want [ALL]", got)
	}
	if !*root.ReadOnlyRootFilesystem || *root.AllowPrivilegeEscalation {
		t.Error("root pvcdump must keep readOnlyRootFilesystem and no privilege escalation")
	}

	app := PVCDumpSecurityContext(&karkivev1alpha1.PVCSourceSpec{RunAsUser: ptr.To(int64(33)), RunAsGroup: ptr.To(int64(34))})
	if *app.RunAsUser != 33 || *app.RunAsGroup != 34 || !*app.RunAsNonRoot {
		t.Errorf("runAsUser override=%#v", app)
	}
	if len(app.Capabilities.Add) != 0 {
		t.Errorf("non-root pvcdump must not add capabilities: %v", app.Capabilities.Add)
	}
	sameGroup := PVCDumpSecurityContext(&karkivev1alpha1.PVCSourceSpec{RunAsUser: ptr.To(int64(1000))})
	if *sameGroup.RunAsGroup != 1000 {
		t.Errorf("runAsGroup should default to runAsUser, got %d", *sameGroup.RunAsGroup)
	}
}

func TestMutateBackupCronJob_PVCConsumerAffinity(t *testing.T) {
	backup := testPVCBackup()
	backup.Spec.PVC.ConsumerSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nextcloud"}}

	cj := &batchv1.CronJob{}
	MutateBackupCronJob(cj, backup, config.Config{})
	terms := requiredTerms(t, cj.Spec.JobTemplate.Spec.Template.Spec.Affinity)
	if len(terms) != 1 || terms[0].TopologyKey != kubernetesHostnameLabel || terms[0].LabelSelector.MatchLabels["app"] != "nextcloud" {
		t.Fatalf("terms=%#v", terms)
	}

	backup.Spec.Runtime = &karkivev1alpha1.RuntimeSpec{Mode: karkivev1alpha1.RuntimeModeCronJobWithVolumeHolder}
	MutateBackupCronJob(cj, backup, config.Config{})
	terms = requiredTerms(t, cj.Spec.JobTemplate.Spec.Template.Spec.Affinity)
	if len(terms) != 2 {
		t.Fatalf("holder + consumer: want 2 required terms, got %#v", terms)
	}
	if terms[0].LabelSelector.MatchLabels[LabelRuntimeRole] != RuntimeRoleVolumeHolder {
		t.Errorf("first term should select the holder: %#v", terms[0].LabelSelector)
	}
	if terms[1].LabelSelector.MatchLabels["app"] != "nextcloud" {
		t.Errorf("second term should select the consumer: %#v", terms[1].LabelSelector)
	}

	deploy := &appsv1.Deployment{}
	MutateVolumeHolderDeployment(deploy, VolumeHolderSpec{
		ClaimName: BackupOwnedName(backup),
		Affinity:  RequiredPodAffinity(PVCConsumerAffinityTerms(backup)...),
	}, config.Config{})
	holderTerms := requiredTerms(t, deploy.Spec.Template.Spec.Affinity)
	if len(holderTerms) != 1 || holderTerms[0].LabelSelector.MatchLabels["app"] != "nextcloud" {
		t.Fatalf("holder should colocate with the consumer: %#v", holderTerms)
	}
}

func TestPVCConsumerAffinityTerms_IgnoredForDatabaseEngines(t *testing.T) {
	backup := testBackup()
	backup.Spec.PVC = &karkivev1alpha1.PVCSourceSpec{
		ClaimName:        "x",
		ConsumerSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}},
	}
	if terms := PVCConsumerAffinityTerms(backup); len(terms) != 0 {
		t.Fatalf("postgres backup got consumer terms %#v", terms)
	}
	if RequiredPodAffinity() != nil {
		t.Fatal("RequiredPodAffinity() with no terms should be nil")
	}
}

func TestEngineImplemented_PVCBackupOnly(t *testing.T) {
	if !EngineImplemented(karkivev1alpha1.EnginePVC) {
		t.Fatal("Backup should accept engine pvc")
	}
	if RestoreEngineImplemented(karkivev1alpha1.EnginePVC) {
		t.Fatal("Restore must not accept engine pvc")
	}
	if !RestoreEngineImplemented(karkivev1alpha1.EngineRedis) || !RestoreEngineImplemented("") {
		t.Fatal("Restore should keep database engines")
	}
	if DumpPrefix(karkivev1alpha1.EnginePVC) != "pvcdump" {
		t.Fatal(DumpPrefix(karkivev1alpha1.EnginePVC))
	}
}

func TestTarImageOverride(t *testing.T) {
	backup := testPVCBackup()
	backup.Spec.Images = &karkivev1alpha1.ImageSet{Tar: &karkivev1alpha1.ImageSpec{Image: "tar:custom", PullPolicy: corev1.PullAlways}}
	cj := &batchv1.CronJob{}
	MutateBackupCronJob(cj, backup, config.Config{TarImage: "tar:operator"})
	dump := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[1]
	if dump.Image != "tar:custom" || dump.ImagePullPolicy != corev1.PullAlways {
		t.Errorf("image=%s pull=%s", dump.Image, dump.ImagePullPolicy)
	}
	MutateBackupCronJob(cj, testPVCBackup(), config.Config{TarImage: "tar:operator"})
	if got := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[1].Image; got != "tar:operator" {
		t.Errorf("operator --tar-image not used: %s", got)
	}
}

func requiredTerms(t *testing.T, aff *corev1.Affinity) []corev1.PodAffinityTerm {
	t.Helper()
	if aff == nil || aff.PodAffinity == nil {
		t.Fatal("expected podAffinity")
	}
	return aff.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
}
