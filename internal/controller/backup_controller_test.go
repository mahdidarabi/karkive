package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
	"github.com/mahdidarabi/KArkive/internal/ptr"
	"github.com/mahdidarabi/KArkive/internal/resources"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := karkivev1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBackupReconcile_CreatesOwnedResources(t *testing.T) {
	scheme := testScheme(t)
	backup := &karkivev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "app-postgres", Namespace: "backup"},
		Spec: karkivev1alpha1.BackupSpec{
			Engine:   karkivev1alpha1.EnginePostgres,
			Schedule: "0 2 * * *",
			Database: karkivev1alpha1.DatabaseSpec{Host: "postgres.example.svc.cluster.local", Name: "app"},
			S3: karkivev1alpha1.S3Spec{
				Endpoint: "https://s3.example.com",
				Bucket:   "backups",
				Path:     "app/pgdump",
			},
			SecretRef: corev1.LocalObjectReference{Name: "backup-creds"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-creds", Namespace: "backup"},
		Data: map[string][]byte{
			"username":       []byte("app"),
			"password":       []byte("secret"),
			"s3_access_key":  []byte("ak"),
			"s3_secret_key":  []byte("sk"),
			"gpg_passphrase": []byte("pgp"),
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()

	r := &BackupReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(16),
		Config:   config.Config{},
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace},
	})
	if err != nil {
		t.Fatal(err)
	}

	cm := &corev1.ConfigMap{}
	owned := resources.BackupOwnedName(backup)
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, cm); err != nil {
		t.Fatalf("configmap: %v", err)
	}
	if cm.Data["PGDATABASE"] != "app" {
		t.Errorf("PGDATABASE=%q", cm.Data["PGDATABASE"])
	}
	if cm.Name != "karkive-backup-app-postgres" {
		t.Errorf("configmap name=%q", cm.Name)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, pvc); err != nil {
		t.Fatalf("pvc: %v", err)
	}

	cj := &batchv1.CronJob{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, cj); err != nil {
		t.Fatalf("cronjob: %v", err)
	}
	if len(cj.Spec.JobTemplate.Spec.Template.Spec.Containers) != 5 {
		t.Fatalf("expected 5 containers, got %d", len(cj.Spec.JobTemplate.Spec.Template.Spec.Containers))
	}

	updated := &karkivev1alpha1.Backup{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(backup), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != karkivev1alpha1.BackupPhaseReady {
		t.Errorf("phase=%q", updated.Status.Phase)
	}
	if updated.Status.CronJobName != owned {
		t.Errorf("status.cronJobName=%q", updated.Status.CronJobName)
	}
	cond := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("ready condition=%v", cond)
	}
	succeeded := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionBackupSucceeded)
	if succeeded == nil || succeeded.Status != metav1.ConditionUnknown {
		t.Errorf("BackupSucceeded=%v", succeeded)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: resources.VolumeHolderName(owned)}, &appsv1.Deployment{}); err == nil {
		t.Fatal("default CronJob runtime must not create a volume holder")
	}
}

func TestBackupReconcile_VolumeHolderPendingThenReady(t *testing.T) {
	scheme := testScheme(t)
	backup := testBackupCR()
	backup.Spec.Runtime = &karkivev1alpha1.RuntimeSpec{Mode: karkivev1alpha1.RuntimeModeCronJobWithVolumeHolder}
	secret := testBackupSecret()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret).
		WithStatusSubresource(&karkivev1alpha1.Backup{}, &appsv1.Deployment{}).
		Build()
	r := &BackupReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(8)}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace}}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != holderRequeue {
		t.Fatalf("requeue=%v, want %v", res.RequeueAfter, holderRequeue)
	}

	owned := resources.BackupOwnedName(backup)
	holderName := resources.VolumeHolderName(owned)
	deploy := &appsv1.Deployment{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: holderName}, deploy); err != nil {
		t.Fatalf("holder: %v", err)
	}
	if deploy.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("strategy=%q", deploy.Spec.Strategy.Type)
	}
	cj := &batchv1.CronJob{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, cj); err != nil {
		t.Fatal(err)
	}
	aff := cj.Spec.JobTemplate.Spec.Template.Spec.Affinity
	if aff == nil || aff.PodAffinity == nil || len(aff.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 1 {
		t.Fatalf("expected required podAffinity, got %#v", aff)
	}

	pending := &karkivev1alpha1.Backup{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(backup), pending); err != nil {
		t.Fatal(err)
	}
	if pending.Status.Phase != karkivev1alpha1.BackupPhasePending {
		t.Errorf("phase=%q, want Pending", pending.Status.Phase)
	}
	if pending.Status.VolumeHolderName != holderName {
		t.Errorf("volumeHolderName=%q", pending.Status.VolumeHolderName)
	}

	deploy.Status.AvailableReplicas = 1
	if err := c.Status().Update(context.Background(), deploy); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	ready := &karkivev1alpha1.Backup{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(backup), ready); err != nil {
		t.Fatal(err)
	}
	if ready.Status.Phase != karkivev1alpha1.BackupPhaseReady {
		t.Errorf("phase=%q, want Ready", ready.Status.Phase)
	}
}

func TestBackupReconcile_S3DisabledSkipsSyncAndS3Keys(t *testing.T) {
	scheme := testScheme(t)
	enabled := false
	backup := &karkivev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "app-postgres", Namespace: "backup"},
		Spec: karkivev1alpha1.BackupSpec{
			Engine:    karkivev1alpha1.EnginePostgres,
			Schedule:  "0 2 * * *",
			Database:  karkivev1alpha1.DatabaseSpec{Host: "postgres.example.svc.cluster.local", Name: "app"},
			S3:        karkivev1alpha1.S3Spec{Enabled: &enabled},
			SecretRef: corev1.LocalObjectReference{Name: "backup-creds"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-creds", Namespace: "backup"},
		Data: map[string][]byte{
			"username":       []byte("app"),
			"password":       []byte("secret"),
			"gpg_passphrase": []byte("pgp"),
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()
	r := &BackupReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(16),
		Config:   config.Config{},
	}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace},
	}); err != nil {
		t.Fatal(err)
	}

	owned := resources.BackupOwnedName(backup)
	cj := &batchv1.CronJob{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, cj); err != nil {
		t.Fatalf("cronjob: %v", err)
	}
	if n := len(cj.Spec.JobTemplate.Spec.Template.Spec.Containers); n != 4 {
		t.Fatalf("expected 4 containers without s3-sync, got %d", n)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, cm); err != nil {
		t.Fatalf("configmap: %v", err)
	}
	if cm.Data["S3_ENABLED"] != "false" {
		t.Errorf("S3_ENABLED=%q", cm.Data["S3_ENABLED"])
	}

	updated := &karkivev1alpha1.Backup{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(backup), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != karkivev1alpha1.BackupPhaseReady {
		t.Errorf("phase=%q", updated.Status.Phase)
	}
}

func TestBackupReconcile_RedisCreatesOwnedResources(t *testing.T) {
	scheme := testScheme(t)
	backup := &karkivev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "cache-redis", Namespace: "backup"},
		Spec: karkivev1alpha1.BackupSpec{
			Engine:   karkivev1alpha1.EngineRedis,
			Schedule: "0 */12 * * *",
			Database: karkivev1alpha1.DatabaseSpec{Host: "redis.example.svc.cluster.local", Name: "cache"},
			S3: karkivev1alpha1.S3Spec{
				Endpoint: "https://s3.example.com",
				Bucket:   "backups",
				Path:     "cache/redisdump",
			},
			SecretRef: corev1.LocalObjectReference{Name: "backup-creds"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-creds", Namespace: "backup"},
		Data: map[string][]byte{
			"username":       []byte("default"),
			"password":       []byte("secret"),
			"s3_access_key":  []byte("ak"),
			"s3_secret_key":  []byte("sk"),
			"gpg_passphrase": []byte("pgp"),
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()
	r := &BackupReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(8),
	}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := &karkivev1alpha1.Backup{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(backup), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != karkivev1alpha1.BackupPhaseReady {
		t.Errorf("phase=%q", updated.Status.Phase)
	}
	cj := &batchv1.CronJob{}
	owned := resources.BackupOwnedName(backup)
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, cj); err != nil {
		t.Fatal(err)
	}
	if cj.Spec.JobTemplate.Spec.Template.Spec.Containers[1].Name != "redisdump" {
		t.Errorf("dump container=%q", cj.Spec.JobTemplate.Spec.Template.Spec.Containers[1].Name)
	}
}

func TestBackupReconcile_CopiesLastJobFailure(t *testing.T) {
	scheme := testScheme(t)
	backup := &karkivev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "app-postgres", Namespace: "backup"},
		Spec: karkivev1alpha1.BackupSpec{
			Engine:   karkivev1alpha1.EnginePostgres,
			Schedule: "0 2 * * *",
			Database: karkivev1alpha1.DatabaseSpec{Host: "postgres.example.svc.cluster.local", Name: "app"},
			S3: karkivev1alpha1.S3Spec{
				Endpoint: "https://s3.example.com",
				Bucket:   "backups",
				Path:     "app/pgdump",
			},
			SecretRef: corev1.LocalObjectReference{Name: "backup-creds"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-creds", Namespace: "backup"},
		Data: map[string][]byte{
			"username":       []byte("app"),
			"password":       []byte("secret"),
			"s3_access_key":  []byte("ak"),
			"s3_secret_key":  []byte("sk"),
			"gpg_passphrase": []byte("pgp"),
		},
	}
	failedAt := metav1.Now()
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "karkive-backup-app-postgres-1",
			Namespace: "backup",
			Labels: map[string]string{
				resources.LabelAppManagedBy: resources.ManagedBy,
				resources.LabelKind:         resources.KindBackup,
				resources.LabelBackupName:   "app-postgres",
			},
		},
		Status: batchv1.JobStatus{
			Failed: 1,
			Conditions: []batchv1.JobCondition{{
				Type:               batchv1.JobFailed,
				Status:             corev1.ConditionTrue,
				Reason:             "BackoffLimitExceeded",
				Message:            "Job has reached the specified backoff limit",
				LastTransitionTime: failedAt,
			}},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret, job).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()
	r := &BackupReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(8),
	}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := &karkivev1alpha1.Backup{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(backup), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.LastJob == nil {
		t.Fatal("expected lastJob")
	}
	if updated.Status.LastJob.Outcome != karkivev1alpha1.LastJobOutcomeFailed {
		t.Errorf("outcome=%q", updated.Status.LastJob.Outcome)
	}
	if updated.Status.LastJob.Reason != "BackoffLimitExceeded" {
		t.Errorf("reason=%q", updated.Status.LastJob.Reason)
	}
	if updated.Status.Phase != karkivev1alpha1.BackupPhaseReady {
		t.Errorf("phase=%q, want Ready (last Job must not overload phase)", updated.Status.Phase)
	}
	ready := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Errorf("Ready=%v", ready)
	}
	succeeded := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionBackupSucceeded)
	if succeeded == nil || succeeded.Status != metav1.ConditionFalse {
		t.Errorf("BackupSucceeded=%v", succeeded)
	}
	if succeeded != nil && succeeded.Reason != "BackoffLimitExceeded" {
		t.Errorf("BackupSucceeded.reason=%q", succeeded.Reason)
	}
}

func TestBackupReconcile_LastJobSuccessSetsBackupSucceeded(t *testing.T) {
	scheme := testScheme(t)
	backup := testBackupCR()
	secret := testBackupSecret()
	done := metav1.Now()
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "karkive-backup-app-postgres-1",
			Namespace: backup.Namespace,
			Labels: map[string]string{
				resources.LabelAppManagedBy: resources.ManagedBy,
				resources.LabelKind:         resources.KindBackup,
				resources.LabelBackupName:   backup.Name,
			},
		},
		Status: batchv1.JobStatus{
			Succeeded:      1,
			StartTime:      &done,
			CompletionTime: &done,
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret, job).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()
	r := &BackupReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(8)}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace},
	}); err != nil {
		t.Fatal(err)
	}
	updated := &karkivev1alpha1.Backup{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(backup), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != karkivev1alpha1.BackupPhaseReady {
		t.Errorf("phase=%q", updated.Status.Phase)
	}
	succeeded := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionBackupSucceeded)
	if succeeded == nil || succeeded.Status != metav1.ConditionTrue {
		t.Errorf("BackupSucceeded=%v", succeeded)
	}
}

func TestBackupReconcile_DoesNotRecreateWhileDeleting(t *testing.T) {
	scheme := testScheme(t)
	now := metav1.Now()
	backup := &karkivev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "app-postgres",
			Namespace:         "backup",
			DeletionTimestamp: &now,
			Finalizers:        []string{"foregroundDeletion"},
		},
		Spec: karkivev1alpha1.BackupSpec{
			Engine:   karkivev1alpha1.EnginePostgres,
			Schedule: "0 2 * * *",
			Database: karkivev1alpha1.DatabaseSpec{Host: "postgres.example.svc.cluster.local", Name: "app"},
			S3: karkivev1alpha1.S3Spec{
				Endpoint: "https://s3.example.com",
				Bucket:   "backups",
				Path:     "app/pgdump",
			},
			SecretRef: corev1.LocalObjectReference{Name: "backup-creds"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-creds", Namespace: "backup"},
		Data: map[string][]byte{
			"username":       []byte("app"),
			"password":       []byte("secret"),
			"s3_access_key":  []byte("ak"),
			"s3_secret_key":  []byte("sk"),
			"gpg_passphrase": []byte("pgp"),
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()
	r := &BackupReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(8),
	}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace},
	}); err != nil {
		t.Fatal(err)
	}
	owned := resources.BackupOwnedName(backup)
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, &batchv1.CronJob{}); err == nil {
		t.Fatal("expected no CronJob while Backup is terminating")
	}
}

func TestBackupReconcile_DeletesLegacyOwnedNames(t *testing.T) {
	scheme := testScheme(t)
	backup := testBackupCR()
	backup.UID = types.UID("backup-uid")
	secret := testBackupSecret()
	legacy := resources.LegacyOwnedName(backup.Name)
	owners := []metav1.OwnerReference{{
		APIVersion: karkivev1alpha1.GroupVersion.String(),
		Kind:       "Backup",
		Name:       backup.Name,
		UID:        backup.UID,
		Controller: ptr.To(true),
	}}
	legacyCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: legacy, Namespace: backup.Namespace, OwnerReferences: owners}}
	legacyCJ := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: legacy, Namespace: backup.Namespace, OwnerReferences: owners}}
	legacyPVC := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: legacy, Namespace: backup.Namespace, OwnerReferences: owners}}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret, legacyCM, legacyCJ, legacyPVC).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()
	r := &BackupReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(8)}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace},
	}); err != nil {
		t.Fatal(err)
	}

	owned := resources.BackupOwnedName(backup)
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: owned}, &batchv1.CronJob{}); err != nil {
		t.Fatalf("new CronJob: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: legacy}, &batchv1.CronJob{}); err == nil {
		t.Fatal("expected legacy CronJob to be deleted")
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: legacy}, &corev1.ConfigMap{}); err == nil {
		t.Fatal("expected legacy ConfigMap to be deleted")
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: legacy}, &corev1.PersistentVolumeClaim{}); err != nil {
		t.Fatalf("legacy PVC should be kept: %v", err)
	}
}

func TestBackupReconcile_SkipsNoopStatusAndEvents(t *testing.T) {
	scheme := testScheme(t)
	backup := testBackupCR()
	secret := testBackupSecret()
	base := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()
	c := &statusCountClient{Client: base}
	rec := record.NewFakeRecorder(16)
	r := &BackupReconciler{Client: c, Scheme: scheme, Recorder: rec}

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if c.updates != 1 {
		t.Fatalf("status updates after first reconcile=%d, want 1", c.updates)
	}
	events := drainEvents(rec)
	if len(events) != 1 || !strings.Contains(events[0], "Synced") {
		t.Fatalf("events after first reconcile=%v", events)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if c.updates != 1 {
		t.Fatalf("status updates after second reconcile=%d, want 1", c.updates)
	}
	if extra := drainEvents(rec); len(extra) != 0 {
		t.Fatalf("unexpected events on second reconcile: %v", extra)
	}
}

func TestBackupReconcile_PatchesLastJobWithoutSyncedEvent(t *testing.T) {
	scheme := testScheme(t)
	backup := testBackupCR()
	secret := testBackupSecret()
	base := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()
	c := &statusCountClient{Client: base}
	rec := record.NewFakeRecorder(16)
	r := &BackupReconciler{Client: c, Scheme: scheme, Recorder: rec}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drainEvents(rec)

	failedAt := metav1.Now()
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "karkive-backup-app-postgres-1",
			Namespace: backup.Namespace,
			Labels: map[string]string{
				resources.LabelAppManagedBy: resources.ManagedBy,
				resources.LabelKind:         resources.KindBackup,
				resources.LabelBackupName:   backup.Name,
			},
		},
		Status: batchv1.JobStatus{
			Failed: 1,
			Conditions: []batchv1.JobCondition{{
				Type:               batchv1.JobFailed,
				Status:             corev1.ConditionTrue,
				Reason:             "BackoffLimitExceeded",
				Message:            "Job has reached the specified backoff limit",
				LastTransitionTime: failedAt,
			}},
		},
	}
	if err := c.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if c.updates != 2 {
		t.Fatalf("status updates=%d, want 2 after lastJob change", c.updates)
	}
	if extra := drainEvents(rec); len(extra) != 0 {
		t.Fatalf("lastJob change must not emit Synced: %v", extra)
	}
	updated := &karkivev1alpha1.Backup{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(backup), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.LastJob == nil || updated.Status.LastJob.Outcome != karkivev1alpha1.LastJobOutcomeFailed {
		t.Fatalf("lastJob=%v", updated.Status.LastJob)
	}
}

func TestBackupReconcile_SecretNotFoundEventsOnce(t *testing.T) {
	scheme := testScheme(t)
	backup := testBackupCR()
	base := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup).
		WithStatusSubresource(&karkivev1alpha1.Backup{}).
		Build()
	c := &statusCountClient{Client: base}
	rec := record.NewFakeRecorder(16)
	r := &BackupReconciler{Client: c, Scheme: scheme, Recorder: rec}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	events := drainEvents(rec)
	if len(events) != 1 || !strings.Contains(events[0], "SecretNotFound") {
		t.Fatalf("events=%v", events)
	}
	if c.updates != 1 {
		t.Fatalf("status updates=%d, want 1", c.updates)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if extra := drainEvents(rec); len(extra) != 0 {
		t.Fatalf("repeated SecretNotFound events: %v", extra)
	}
	if c.updates != 1 {
		t.Fatalf("status updates after second reconcile=%d, want 1", c.updates)
	}
}

func testBackupCR() *karkivev1alpha1.Backup {
	return &karkivev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "app-postgres", Namespace: "backup", Generation: 1},
		Spec: karkivev1alpha1.BackupSpec{
			Engine:   karkivev1alpha1.EnginePostgres,
			Schedule: "0 2 * * *",
			Database: karkivev1alpha1.DatabaseSpec{Host: "postgres.example.svc.cluster.local", Name: "app"},
			S3: karkivev1alpha1.S3Spec{
				Endpoint: "https://s3.example.com",
				Bucket:   "backups",
				Path:     "app/pgdump",
			},
			SecretRef: corev1.LocalObjectReference{Name: "backup-creds"},
		},
	}
}

func testBackupSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-creds", Namespace: "backup"},
		Data: map[string][]byte{
			"username":       []byte("app"),
			"password":       []byte("secret"),
			"s3_access_key":  []byte("ak"),
			"s3_secret_key":  []byte("sk"),
			"gpg_passphrase": []byte("pgp"),
		},
	}
}

type statusCountClient struct {
	client.Client
	updates int
}

func (c *statusCountClient) Status() client.SubResourceWriter {
	return &statusCountWriter{SubResourceWriter: c.Client.Status(), n: &c.updates}
}

type statusCountWriter struct {
	client.SubResourceWriter
	n *int
}

func (w *statusCountWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	*w.n++
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func (w *statusCountWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	*w.n++
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func drainEvents(rec *record.FakeRecorder) []string {
	var out []string
	for {
		select {
		case e := <-rec.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}
