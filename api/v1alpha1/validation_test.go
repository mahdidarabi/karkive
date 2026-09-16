package v1alpha1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
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
}

func boolPtr(v bool) *bool { return &v }
