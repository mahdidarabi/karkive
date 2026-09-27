package v1alpha1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateBackupSpec(t *testing.T) {
	ok := BackupSpec{
		Schedule:  "0 2 * * *",
		Database:  DatabaseSpec{Host: "postgres.example.svc.cluster.local", Name: "app"},
		S3:        S3Spec{Path: "app/pgdump"},
		SecretRef: corev1.LocalObjectReference{Name: "backup-creds"},
	}
	if err := ValidateBackupSpec(ok); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Schedule = "not a cron"
	if err := ValidateBackupSpec(bad); err == nil {
		t.Fatal("expected invalid schedule")
	}
	missing := ok
	missing.SecretRef.Name = ""
	if err := ValidateBackupSpec(missing); err == nil {
		t.Fatal("expected missing secretRef")
	}

	local := ok
	local.S3 = S3Spec{Enabled: boolPtr(false)}
	if err := ValidateBackupSpec(local); err != nil {
		t.Fatal(err)
	}
	local.Persistence = &PersistenceSpec{Enabled: boolPtr(false)}
	if err := ValidateBackupSpec(local); err == nil {
		t.Fatal("expected persistence when S3 is disabled")
	}
	noPath := ok
	noPath.S3.Path = ""
	if err := ValidateBackupSpec(noPath); err == nil {
		t.Fatal("expected s3.path when S3 is enabled")
	}

	holder := ok
	holder.Runtime = &RuntimeSpec{Mode: RuntimeModeCronJobWithVolumeHolder}
	if err := ValidateBackupSpec(holder); err != nil {
		t.Fatal(err)
	}
	holder.Persistence = &PersistenceSpec{Enabled: boolPtr(false)}
	if err := ValidateBackupSpec(holder); err == nil {
		t.Fatal("expected persistence for CronJobWithVolumeHolder")
	}
	unimpl := ok
	unimpl.Runtime = &RuntimeSpec{Mode: RuntimeModePersistentPodWithTriggerJob}
	if err := ValidateBackupSpec(unimpl); err == nil {
		t.Fatal("expected reject for PersistentPodWithTriggerJob")
	}
	unimpl.Runtime.Mode = RuntimeModePersistentPodWithInPodCron
	if err := ValidateBackupSpec(unimpl); err == nil {
		t.Fatal("expected reject for PersistentPodWithInPodCron")
	}
}

func TestValidateBackupSpec_PVC(t *testing.T) {
	ok := BackupSpec{
		Engine:    EnginePVC,
		Schedule:  "0 3 * * *",
		PVC:       &PVCSourceSpec{ClaimName: "nextcloud-data", Path: "data/", Excludes: []string{"cache", "./tmp"}},
		S3:        S3Spec{Path: "nextcloud/pvcdump"},
		SecretRef: corev1.LocalObjectReference{Name: "backup-creds"},
	}
	if err := ValidateBackupSpec(ok); err != nil {
		t.Fatalf("pvc backup without database should be valid: %v", err)
	}

	for name, mutate := range map[string]func(*BackupSpec){
		"missing pvc":       func(s *BackupSpec) { s.PVC = nil },
		"missing claimName": func(s *BackupSpec) { s.PVC = &PVCSourceSpec{} },
		"absolute path":     func(s *BackupSpec) { s.PVC.Path = "/data" },
		"parent path":       func(s *BackupSpec) { s.PVC.Path = "data/../../etc" },
		"dotdot path":       func(s *BackupSpec) { s.PVC.Path = ".." },
		"empty exclude":     func(s *BackupSpec) { s.PVC.Excludes = []string{" "} },
		"multiline exclude": func(s *BackupSpec) { s.PVC.Excludes = []string{"a\nb"} },
		"empty selector":    func(s *BackupSpec) { s.PVC.ConsumerSelector = &metav1.LabelSelector{} },
		"bad selector": func(s *BackupSpec) {
			s.PVC.ConsumerSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: "Nope"}}}
		},
	} {
		spec := ok
		spec.PVC = ok.PVC.DeepCopy()
		mutate(&spec)
		if err := ValidateBackupSpec(spec); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}

	withSelector := ok
	withSelector.PVC = ok.PVC.DeepCopy()
	withSelector.PVC.ConsumerSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nextcloud"}}
	if err := ValidateBackupSpec(withSelector); err != nil {
		t.Fatal(err)
	}
	nestedDots := ok
	nestedDots.PVC = &PVCSourceSpec{ClaimName: "c", Path: "a/../b"}
	if err := ValidateBackupSpec(nestedDots); err == nil {
		t.Fatal("expected any .. segment to be rejected")
	}
	dotted := ok
	dotted.PVC = &PVCSourceSpec{ClaimName: "c", Path: "./a..b/c"}
	if err := ValidateBackupSpec(dotted); err != nil {
		t.Fatalf("./a..b/c has no .. segment: %v", err)
	}

	db := BackupSpec{
		Schedule:  "0 2 * * *",
		Database:  DatabaseSpec{Host: "postgres", Name: "app"},
		PVC:       &PVCSourceSpec{ClaimName: "data"},
		S3:        S3Spec{Path: "app/pgdump"},
		SecretRef: corev1.LocalObjectReference{Name: "backup-creds"},
	}
	if err := ValidateBackupSpec(db); err == nil {
		t.Fatal("expected spec.pvc to be rejected for database engines")
	}
	db.PVC = nil
	db.Database = DatabaseSpec{}
	if err := ValidateBackupSpec(db); err == nil {
		t.Fatal("database engines still require spec.database")
	}
}

func TestValidateRestoreSpec(t *testing.T) {
	ok := RestoreSpec{
		Engine:         EnginePostgres,
		Schedule:       "30 2 * * *",
		Database:       DatabaseSpec{Host: "postgres.example.svc.cluster.local", Name: "app"},
		S3:             S3Spec{Path: "app/pgdump"},
		SecretRef:      corev1.LocalObjectReference{Name: "restore-creds"},
		PostgresSecret: &SecretKeySelector{Name: "postgres"},
	}
	if err := ValidateRestoreSpec(ok); err != nil {
		t.Fatal(err)
	}
	redis := ok
	redis.Engine = EngineRedis
	redis.PostgresSecret = nil
	if err := ValidateRestoreSpec(redis); err == nil {
		t.Fatal("expected redisSecret")
	}
	redis.RedisSecret = &SecretKeySelector{Name: "redis"}
	if err := ValidateRestoreSpec(redis); err != nil {
		t.Fatal(err)
	}
	disabled := ok
	disabled.S3.Enabled = boolPtr(false)
	if err := ValidateRestoreSpec(disabled); err == nil {
		t.Fatal("expected reject when restore s3.enabled is false")
	}
	lite := ok
	lite.Persistence = &PersistenceSpec{Enabled: boolPtr(false)}
	lite.Runtime = &RuntimeSpec{Mode: RuntimeModeCronJobWithVolumeHolder}
	if err := ValidateRestoreSpec(lite); err == nil {
		t.Fatal("expected reject VolumeHolder without persistence")
	}
	pvc := ok
	pvc.Engine = EnginePVC
	if err := ValidateRestoreSpec(pvc); err == nil {
		t.Fatal("expected reject for engine pvc restore")
	}
}

func boolPtr(v bool) *bool { return &v }
