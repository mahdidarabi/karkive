package resources

import (
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
)

// MutateBackupConfigMap writes the pipeline environment into cm.
func MutateBackupConfigMap(cm *corev1.ConfigMap, backup *karkivev1alpha1.Backup, cfg config.Config) error {
	engine := EffectiveEngine(backup.Spec.Engine)
	port := DatabasePort(backup.Spec.Database, engine)

	cm.Labels = BackupLabels(backup)
	cm.Data = map[string]string{
		"ENGINE":               string(engine),
		"DUMP_PREFIX":          DumpPrefix(engine),
		"DATA_DIR":             dataDir(backup),
		"MC_CONFIG_DIR":        mcConfigDir(backup),
		"LOCAL_RETENTION_DAYS": strconv.Itoa(int(localRetentionDays(backup))),
		"S3_ENABLED":           boolEnv(backup.Spec.S3.Enabled, true),
		"LOG_FILE_ENABLED":     boolEnv(backup.Spec.LogFileEnabled, false),
	}
	if backup.Spec.S3.EnabledOrDefault() {
		cm.Data["S3_ENDPOINT"] = s3Endpoint(backup, cfg)
		cm.Data["S3_BUCKET"] = s3Bucket(backup, cfg)
		cm.Data["S3_PATH"] = backup.Spec.S3.Path
		cm.Data["S3_RETENTION_DAYS"] = strconv.Itoa(int(s3RetentionDays(backup)))
	}

	switch engine {
	case karkivev1alpha1.EngineMariaDB:
		cm.Data["MYSQL_HOST"] = backup.Spec.Database.Host
		cm.Data["MYSQL_PORT"] = strconv.Itoa(int(port))
		cm.Data["MYSQL_DATABASE"] = backup.Spec.Database.Name
	case karkivev1alpha1.EngineRedis:
		cm.Data["REDIS_HOST"] = backup.Spec.Database.Host
		cm.Data["REDIS_PORT"] = strconv.Itoa(int(port))
		cm.Data["REDIS_NAME"] = backup.Spec.Database.Name
	case karkivev1alpha1.EnginePVC:
		src := pvcSource(backup)
		cm.Data["PVC_CLAIM_NAME"] = src.ClaimName
		cm.Data["PVC_SOURCE_DIR"] = config.DefaultPVCSourceDir
		cm.Data["PVC_PATH"] = pvcSourcePath(src)
		cm.Data["PVC_EXCLUDES"] = strings.Join(src.Excludes, "\n")
	default:
		cm.Data["PGHOST"] = backup.Spec.Database.Host
		cm.Data["PGPORT"] = strconv.Itoa(int(port))
		cm.Data["PGDATABASE"] = backup.Spec.Database.Name
	}

	if backup.Spec.S3.EnabledOrDefault() {
		if cm.Data["S3_ENDPOINT"] == "" {
			return fmt.Errorf("s3.endpoint is empty (set spec.s3.endpoint or --default-s3-endpoint)")
		}
		if cm.Data["S3_BUCKET"] == "" {
			return fmt.Errorf("s3.bucket is empty (set spec.s3.bucket or --default-s3-bucket)")
		}
	}
	return nil
}
