package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/resources"
)

func testPVCBackupCR() *karkivev1alpha1.Backup {
	return &karkivev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "nextcloud-data", Namespace: "nextcloud"},
		Spec: karkivev1alpha1.BackupSpec{
			Engine:   karkivev1alpha1.EnginePVC,
			Schedule: "0 3 * * *",
			PVC:      &karkivev1alpha1.PVCSourceSpec{ClaimName: "nextcloud-data"},
			S3: karkivev1alpha1.S3Spec{
				Endpoint: "https://s3.example.com",
				Bucket:   "backups",
				Path:     "nextcloud/pvcdump",
			},
			SecretRef: corev1.LocalObjectReference{Name: "backup-nextcloud-data"},
		},
	}
}

// No username/password: engine pvc has no database login.
func testPVCBackupSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-nextcloud-data", Namespace: "nextcloud"},
		Data: map[string][]byte{
			"s3_access_key":  []byte("ak"),
			"s3_secret_key":  []byte("sk"),
			"gpg_passphrase": []byte("pgp"),
		},
	}
}

func testSourcePVC(modes ...corev1.PersistentVolumeAccessMode) *corev1.PersistentVolumeClaim {
	if len(modes) == 0 {
		modes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "nextcloud-data", Namespace: "nextcloud"},
		Spec:       corev1.PersistentVolumeClaimSpec{AccessModes: modes},
	}
}

func reconcilePVCBackup(t *testing.T, objs ...client.Object) (client.Client, reconcile.Result, *karkivev1alpha1.Backup) {
	t.Helper()
	scheme := testScheme(t)
	backup := objs[0].(*karkivev1alpha1.Backup)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&karkivev1alpha1.Backup{}, &appsv1.Deployment{}).
		Build()
	r := &BackupReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(16)}
	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := &karkivev1alpha1.Backup{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(backup), updated); err != nil {
		t.Fatal(err)
	}
	return c, res, updated
}

func TestBackupReconcile_PVCCreatesOwnedResources(t *testing.T) {
	backup := testPVCBackupCR()
	c, _, updated := reconcilePVCBackup(t, backup, testPVCBackupSecret(), testSourcePVC())

	if updated.Status.Phase != karkivev1alpha1.BackupPhaseReady {
		t.Fatalf("phase=%q, conditions=%v", updated.Status.Phase, updated.Status.Conditions)
	}
	owned := resources.BackupOwnedName(backup)
	cm := &corev1.ConfigMap{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, cm); err != nil {
		t.Fatalf("configmap: %v", err)
	}
	if cm.Data["ENGINE"] != "pvc" || cm.Data["PVC_CLAIM_NAME"] != "nextcloud-data" {
		t.Errorf("configmap data=%v", cm.Data)
	}
	cj := &batchv1.CronJob{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, cj); err != nil {
		t.Fatalf("cronjob: %v", err)
	}
	if got := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[1].Name; got != "pvcdump" {
		t.Errorf("dump container=%q", got)
	}
	// The source claim is referenced, never owned or rewritten.
	src := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: "nextcloud-data"}, src); err != nil {
		t.Fatal(err)
	}
	if len(src.OwnerReferences) != 0 {
		t.Errorf("source PVC must not get owner refs: %v", src.OwnerReferences)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, &corev1.PersistentVolumeClaim{}); err != nil {
		t.Errorf("scratch PVC %s: %v", owned, err)
	}
}

func TestBackupReconcile_PVCSourceMissingIsPending(t *testing.T) {
	_, res, updated := reconcilePVCBackup(t, testPVCBackupCR(), testPVCBackupSecret())
	if updated.Status.Phase != karkivev1alpha1.BackupPhasePending {
		t.Fatalf("phase=%q", updated.Status.Phase)
	}
	if res.RequeueAfter != sourceRequeue {
		t.Errorf("requeue=%v, want %v", res.RequeueAfter, sourceRequeue)
	}
	cond := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != "SourcePVCNotFound" {
		t.Errorf("ready condition=%v", cond)
	}
}

func TestBackupReconcile_PVCRejectsReadWriteOncePod(t *testing.T) {
	_, _, updated := reconcilePVCBackup(t, testPVCBackupCR(), testPVCBackupSecret(), testSourcePVC(corev1.ReadWriteOncePod))
	if updated.Status.Phase != karkivev1alpha1.BackupPhaseError {
		t.Fatalf("phase=%q", updated.Status.Phase)
	}
	cond := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != "SourcePVCInvalid" {
		t.Errorf("ready condition=%v", cond)
	}
}

func TestBackupReconcile_PVCRejectsOwnScratchClaim(t *testing.T) {
	backup := testPVCBackupCR()
	backup.Spec.PVC.ClaimName = resources.BackupOwnedName(backup)
	_, _, updated := reconcilePVCBackup(t, backup, testPVCBackupSecret())
	cond := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionReady)
	if updated.Status.Phase != karkivev1alpha1.BackupPhaseError || cond == nil || cond.Reason != "SourcePVCInvalid" {
		t.Fatalf("phase=%q condition=%v", updated.Status.Phase, cond)
	}
}

func TestBackupReconcile_DatabaseEnginesStillRequireLoginKeys(t *testing.T) {
	backup := testBackupCR()
	secret := testBackupSecret()
	delete(secret.Data, "username")
	_, _, updated := reconcilePVCBackup(t, backup, secret)
	cond := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionReady)
	if updated.Status.Phase != karkivev1alpha1.BackupPhaseError || cond == nil || cond.Reason != "SecretInvalid" {
		t.Fatalf("phase=%q condition=%v", updated.Status.Phase, cond)
	}
}

func TestBackupReconcile_PVCHolderColocatesWithConsumer(t *testing.T) {
	backup := testPVCBackupCR()
	backup.Spec.Runtime = &karkivev1alpha1.RuntimeSpec{Mode: karkivev1alpha1.RuntimeModeCronJobWithVolumeHolder}
	backup.Spec.PVC.ConsumerSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nextcloud"}}
	c, _, _ := reconcilePVCBackup(t, backup, testPVCBackupSecret(), testSourcePVC())

	deploy := &appsv1.Deployment{}
	name := resources.VolumeHolderName(resources.BackupOwnedName(backup))
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: name}, deploy); err != nil {
		t.Fatalf("holder: %v", err)
	}
	aff := deploy.Spec.Template.Spec.Affinity
	if aff == nil || aff.PodAffinity == nil || len(aff.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 1 {
		t.Fatalf("holder affinity=%#v", aff)
	}
	if got := aff.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].LabelSelector.MatchLabels["app"]; got != "nextcloud" {
		t.Errorf("holder should colocate with app=nextcloud, got %q", got)
	}
}

func TestRestoreReconcile_PVCEngineUnsupported(t *testing.T) {
	scheme := testScheme(t)
	restore := &karkivev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "nextcloud-data", Namespace: "nextcloud"},
		Spec: karkivev1alpha1.RestoreSpec{
			Engine:    karkivev1alpha1.EnginePVC,
			Schedule:  "30 3 * * *",
			S3:        karkivev1alpha1.S3Spec{Path: "nextcloud/pvcdump"},
			SecretRef: corev1.LocalObjectReference{Name: "restore-creds"},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(restore).
		WithStatusSubresource(&karkivev1alpha1.Restore{}).
		Build()
	r := &RestoreReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(8)}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: restore.Name, Namespace: restore.Namespace},
	}); err != nil {
		t.Fatal(err)
	}
	updated := &karkivev1alpha1.Restore{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(restore), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != karkivev1alpha1.RestorePhaseUnsupported {
		t.Fatalf("phase=%q", updated.Status.Phase)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: restore.Namespace, Name: resources.RestoreOwnedName(restore)}, &batchv1.CronJob{}); err == nil {
		t.Fatal("unsupported restore must not create a CronJob")
	}
}
