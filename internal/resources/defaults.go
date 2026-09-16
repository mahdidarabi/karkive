package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
	"github.com/mahdidarabi/KArkive/internal/ptr"
)

func DefaultCleanupResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("20m"),
			corev1.ResourceMemory:           resource.MustParse("32Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("100m"),
			corev1.ResourceMemory:           resource.MustParse("64Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("50Mi"),
		},
	}
}

func DefaultDumpResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("100m"),
			corev1.ResourceMemory:           resource.MustParse("128Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("50Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("150m"),
			corev1.ResourceMemory:           resource.MustParse("192Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("2Gi"),
		},
	}
}

func DefaultCompressEncryptResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("100m"),
			corev1.ResourceMemory:           resource.MustParse("128Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("500m"),
			corev1.ResourceMemory:           resource.MustParse("256Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("100Mi"),
		},
	}
}

func DefaultS3SyncResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("50m"),
			corev1.ResourceMemory:           resource.MustParse("64Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("100m"),
			corev1.ResourceMemory:           resource.MustParse("128Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("100Mi"),
		},
	}
}

func DefaultFetchResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("50m"),
			corev1.ResourceMemory:           resource.MustParse("64Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("200m"),
			corev1.ResourceMemory:           resource.MustParse("128Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("2Gi"),
		},
	}
}

func DefaultDecryptExtractResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("100m"),
			corev1.ResourceMemory:           resource.MustParse("128Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("500m"),
			corev1.ResourceMemory:           resource.MustParse("256Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("2Gi"),
		},
	}
}

func DefaultRestoreStageResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("100m"),
			corev1.ResourceMemory:           resource.MustParse("256Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("1"),
			corev1.ResourceMemory:           resource.MustParse("1Gi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("2Gi"),
		},
	}
}

func mergeResources(base corev1.ResourceRequirements, override *corev1.ResourceRequirements) corev1.ResourceRequirements {
	if override == nil {
		return base
	}
	out := *base.DeepCopy()
	if len(override.Requests) > 0 {
		if out.Requests == nil {
			out.Requests = corev1.ResourceList{}
		}
		for k, v := range override.Requests {
			out.Requests[k] = v
		}
	}
	if len(override.Limits) > 0 {
		if out.Limits == nil {
			out.Limits = corev1.ResourceList{}
		}
		for k, v := range override.Limits {
			out.Limits[k] = v
		}
	}
	return out
}

func restoreStageResources(restore *karkivev1alpha1.Restore) (cleanup, fetch, decrypt, extract, restoreRes corev1.ResourceRequirements) {
	var res *karkivev1alpha1.RestoreResources
	if restore.Spec.Resources != nil {
		res = restore.Spec.Resources
	} else {
		res = &karkivev1alpha1.RestoreResources{}
	}
	cleanup = mergeResources(DefaultCleanupResources(), res.Cleanup)
	fetch = mergeResources(DefaultFetchResources(), res.Fetch)
	decrypt = mergeResources(DefaultDecryptExtractResources(), res.Decrypt)
	extract = mergeResources(DefaultDecryptExtractResources(), res.Extract)
	restoreRes = mergeResources(DefaultRestoreStageResources(), res.Restore)
	return
}

func backupStageResources(backup *karkivev1alpha1.Backup) (cleanup, dump, compress, encrypt, s3sync corev1.ResourceRequirements) {
	var res *karkivev1alpha1.BackupResources
	if backup.Spec.Resources != nil {
		res = backup.Spec.Resources
	} else {
		res = &karkivev1alpha1.BackupResources{}
	}
	cleanup = mergeResources(DefaultCleanupResources(), res.Cleanup)
	dump = mergeResources(DefaultDumpResources(), res.Dump)
	compress = mergeResources(DefaultCompressEncryptResources(), res.Compress)
	encrypt = mergeResources(DefaultCompressEncryptResources(), res.Encrypt)
	s3sync = mergeResources(DefaultS3SyncResources(), res.S3Sync)
	return
}

func PersistenceEnabled(p *karkivev1alpha1.PersistenceSpec) bool {
	if p == nil || p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

func persistenceSize(p *karkivev1alpha1.PersistenceSpec) resource.Quantity {
	if p != nil && !p.Size.IsZero() {
		return p.Size
	}
	return resource.MustParse("1Gi")
}

func postgresImage(images *karkivev1alpha1.ImageSet, cfg config.Config) (string, corev1.PullPolicy) {
	return pipelineImage(images, func(i *karkivev1alpha1.ImageSet) *karkivev1alpha1.ImageSpec {
		return i.Postgres
	}, cfg.PostgresImage, config.DefaultPostgresImage)
}

func mcImage(images *karkivev1alpha1.ImageSet, cfg config.Config) (string, corev1.PullPolicy) {
	return pipelineImage(images, func(i *karkivev1alpha1.ImageSet) *karkivev1alpha1.ImageSpec {
		return i.Mc
	}, cfg.McImage, config.DefaultMcImage)
}

func busyBoxImage(images *karkivev1alpha1.ImageSet, cfg config.Config) (string, corev1.PullPolicy) {
	return pipelineImage(images, func(i *karkivev1alpha1.ImageSet) *karkivev1alpha1.ImageSpec {
		return i.BusyBox
	}, cfg.BusyBoxImage, config.DefaultBusyBoxImage)
}

func gnuPGImage(images *karkivev1alpha1.ImageSet, cfg config.Config) (string, corev1.PullPolicy) {
	return pipelineImage(images, func(i *karkivev1alpha1.ImageSet) *karkivev1alpha1.ImageSpec {
		return i.GnuPG
	}, cfg.GnuPGImage, config.DefaultGnuPGImage)
}

func mariadbImage(images *karkivev1alpha1.ImageSet, cfg config.Config) (string, corev1.PullPolicy) {
	return pipelineImage(images, func(i *karkivev1alpha1.ImageSet) *karkivev1alpha1.ImageSpec {
		return i.MariaDB
	}, cfg.MariaDBImage, config.DefaultMariaDBImage)
}

func redisImage(images *karkivev1alpha1.ImageSet, cfg config.Config) (string, corev1.PullPolicy) {
	return pipelineImage(images, func(i *karkivev1alpha1.ImageSet) *karkivev1alpha1.ImageSpec {
		return i.Redis
	}, cfg.RedisImage, config.DefaultRedisImage)
}

func pipelineImage(images *karkivev1alpha1.ImageSet, pick func(*karkivev1alpha1.ImageSet) *karkivev1alpha1.ImageSpec, configured, fallback string) (string, corev1.PullPolicy) {
	var override *karkivev1alpha1.ImageSpec
	if images != nil {
		override = pick(images)
	}
	return resolveImage(override, configured, fallback)
}

func resolveImage(override *karkivev1alpha1.ImageSpec, configured, fallback string) (string, corev1.PullPolicy) {
	image := firstNonEmpty(configured, fallback)
	pull := corev1.PullIfNotPresent
	if override != nil {
		if override.Image != "" {
			image = override.Image
		}
		if override.PullPolicy != "" {
			pull = override.PullPolicy
		}
	}
	return image, pull
}

func s3Endpoint(backup *karkivev1alpha1.Backup, cfg config.Config) string {
	return firstNonEmpty(backup.Spec.S3.Endpoint, cfg.DefaultS3Endpoint)
}

func s3Bucket(backup *karkivev1alpha1.Backup, cfg config.Config) string {
	return firstNonEmpty(backup.Spec.S3.Bucket, cfg.DefaultS3Bucket)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func localRetentionDays(backup *karkivev1alpha1.Backup) int32 {
	return ptr.Deref(backup.Spec.LocalRetentionDays, config.DefaultLocalRetentionDays)
}

func s3RetentionDays(backup *karkivev1alpha1.Backup) int32 {
	return ptr.Deref(backup.Spec.S3.RetentionDays, config.DefaultS3RetentionDays)
}

func BackupVolumeMountPath(backup *karkivev1alpha1.Backup) string {
	return dataDir(backup)
}

func RestoreVolumeMountPath(restore *karkivev1alpha1.Restore) string {
	return restoreWorkdir(restore)
}

func dataDir(backup *karkivev1alpha1.Backup) string {
	return firstNonEmpty(backup.Spec.DataDir, config.DefaultDataDir)
}

func mcConfigDir(backup *karkivev1alpha1.Backup) string {
	return firstNonEmpty(backup.Spec.McConfigDir, config.DefaultMcConfigDir)
}

func secretName(backup *karkivev1alpha1.Backup) string {
	return backup.Spec.SecretRef.Name
}

func restoreS3Endpoint(restore *karkivev1alpha1.Restore, cfg config.Config) string {
	return firstNonEmpty(restore.Spec.S3.Endpoint, cfg.DefaultS3Endpoint)
}

func restoreS3Bucket(restore *karkivev1alpha1.Restore, cfg config.Config) string {
	return firstNonEmpty(restore.Spec.S3.Bucket, cfg.DefaultS3Bucket)
}

func restoreWorkdir(restore *karkivev1alpha1.Restore) string {
	return firstNonEmpty(restore.Spec.Workdir, config.DefaultWorkdir)
}

func restoreMcConfigDir(restore *karkivev1alpha1.Restore) string {
	return firstNonEmpty(restore.Spec.McConfigDir, config.DefaultMcConfigDir)
}

func restoreOwnerRole(restore *karkivev1alpha1.Restore) string {
	return firstNonEmpty(restore.Spec.Database.OwnerRole, restore.Spec.Database.Name)
}

func restoreSecretName(restore *karkivev1alpha1.Restore) string {
	return restore.Spec.SecretRef.Name
}

func RestorePostgresSecret(restore *karkivev1alpha1.Restore) (name, userKey, passKey string) {
	return restoreSecretKeys(restore.Spec.PostgresSecret, "postgres")
}

func RestoreMariaDBSecret(restore *karkivev1alpha1.Restore) (name, userKey, passKey string) {
	return restoreSecretKeys(restore.Spec.MariaDBSecret, "mariadb")
}

func RestoreRedisSecret(restore *karkivev1alpha1.Restore) (name, userKey, passKey string) {
	return restoreSecretKeys(restore.Spec.RedisSecret, "redis")
}

func RestoreTargetSecret(restore *karkivev1alpha1.Restore) (name, userKey, passKey string) {
	switch EffectiveEngine(restore.Spec.Engine) {
	case karkivev1alpha1.EngineMariaDB:
		return RestoreMariaDBSecret(restore)
	case karkivev1alpha1.EngineRedis:
		return RestoreRedisSecret(restore)
	default:
		return RestorePostgresSecret(restore)
	}
}

func restoreSecretKeys(s *karkivev1alpha1.SecretKeySelector, defaultName string) (name, userKey, passKey string) {
	name, userKey, passKey = defaultName, "username", "password"
	if s != nil {
		if s.Name != "" {
			name = s.Name
		}
		if s.UsernameKey != "" {
			userKey = s.UsernameKey
		}
		if s.PasswordKey != "" {
			passKey = s.PasswordKey
		}
	}
	return
}

func boolEnv(p *bool, def bool) string {
	v := def
	if p != nil {
		v = *p
	}
	if v {
		return "true"
	}
	return "false"
}
