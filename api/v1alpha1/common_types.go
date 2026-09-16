package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Engine identifies the datastore being backed up or restored.
// +kubebuilder:validation:Enum=postgres;mariadb;redis
type Engine string

const (
	EnginePostgres Engine = "postgres"
	EngineMariaDB  Engine = "mariadb"
	EngineRedis    Engine = "redis"
)

// DatabaseSpec is the connection target for dump or restore.
type DatabaseSpec struct {
	// Host is the DNS name or IP of the database (required).
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host"`

	// Port of the database. Defaults: postgres 5432, mariadb 3306, redis 6379.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`

	// Name is the database name (postgres/mariadb) or a logical label (redis).
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// OwnerRole is created/used on postgres restore (defaults to Name).
	OwnerRole string `json:"ownerRole,omitempty"`
}

// S3Spec is the object-storage destination (or source, for restore).
type S3Spec struct {
	// Enabled uploads encrypted dumps to S3 (backup) or fetches them from S3
	// (restore). Default true. When false on Backup, skip s3-sync; dumps stay
	// in retained/ on the PVC. Restore from local retained dumps is not
	// implemented — Restore still requires S3.
	Enabled *bool `json:"enabled,omitempty"`

	// Endpoint is the S3 API URL, e.g. https://s3.example.com.
	// Falls back to the operator --default-s3-endpoint flag when empty.
	Endpoint string `json:"endpoint,omitempty"`

	// Bucket is the S3 bucket name.
	// Falls back to the operator --default-s3-bucket flag when empty.
	Bucket string `json:"bucket,omitempty"`

	// Path is the key prefix inside the bucket, e.g. app/pgdump.
	// Required when Enabled is true (the default).
	Path string `json:"path,omitempty"`

	// RetentionDays is how long encrypted dumps are kept in S3 (backup only). Default 14.
	// +kubebuilder:validation:Minimum=1
	RetentionDays *int32 `json:"retentionDays,omitempty"`
}

// EnabledOrDefault is true unless Enabled is explicitly false.
func (s S3Spec) EnabledOrDefault() bool {
	if s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

// PersistenceSpec controls scratch storage for the pipeline.
// Backup defaults to a PVC so retained/ dumps survive across Jobs.
// Lite jobs may set enabled=false to use emptyDir instead.
type PersistenceSpec struct {
	// Enabled creates and mounts a PVC when true (default true for Backup).
	Enabled *bool `json:"enabled,omitempty"`

	// StorageClassName of the PVC. Empty uses the cluster default.
	StorageClassName string `json:"storageClassName,omitempty"`

	// AccessModes of the PVC. Default [ReadWriteOnce].
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`

	// Size of the PVC, or emptyDir.sizeLimit when Enabled=false. Default 1Gi.
	Size resource.Quantity `json:"size,omitempty"`

	// Annotations copied onto the PVC.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ImageSpec is a full image reference plus pull policy.
type ImageSpec struct {
	// Image is registry/repository:tag.
	Image string `json:"image,omitempty"`

	// PullPolicy for the container. Default IfNotPresent.
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`
}

// ImageSet overrides pipeline container images for Backup and Restore.
type ImageSet struct {
	// BusyBox is used for cleanup, compress, and extract (find, gzip).
	BusyBox *ImageSpec `json:"busybox,omitempty"`
	// GnuPG is used for encrypt and decrypt.
	GnuPG *ImageSpec `json:"gnupg,omitempty"`
	// Postgres is used for pg_dump / pg_restore / psql.
	Postgres *ImageSpec `json:"postgres,omitempty"`
	// Mc is the minio client image used for s3-sync and fetch.
	Mc *ImageSpec `json:"mc,omitempty"`
	// MariaDB is used for mysqldump / mysql restore.
	MariaDB *ImageSpec `json:"mariadb,omitempty"`
	// Redis is used for redis-cli dump and restore.
	Redis *ImageSpec `json:"redis,omitempty"`
}

// SecretKeySelector points at a Secret and optional key names.
type SecretKeySelector struct {
	// Name of the Secret in the same namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// UsernameKey in the Secret. Default "username".
	UsernameKey string `json:"usernameKey,omitempty"`

	// PasswordKey in the Secret. Default "password" (postgres) or engine-specific.
	PasswordKey string `json:"passwordKey,omitempty"`
}

// RuntimeMode selects how pipeline pods are scheduled relative to the PVC.
// +kubebuilder:validation:Enum=CronJob;CronJobWithVolumeHolder;PersistentPodWithTriggerJob;PersistentPodWithInPodCron
type RuntimeMode string

const (
	// RuntimeModeCronJob is a new Job pod every schedule (CSI attach/detach each run). Default.
	RuntimeModeCronJob RuntimeMode = "CronJob"
	// RuntimeModeCronJobWithVolumeHolder keeps a pause Deployment that holds the
	// PVC attached on one node. Job pods colocate with required podAffinity and
	// only NodePublish. Use when re-attaching the claim after a Job exits fails
	// even though no consumer pod exists (not an RWX / multi-writer issue).
	RuntimeModeCronJobWithVolumeHolder RuntimeMode = "CronJobWithVolumeHolder"
	// RuntimeModePersistentPodWithTriggerJob is reserved (not implemented).
	RuntimeModePersistentPodWithTriggerJob RuntimeMode = "PersistentPodWithTriggerJob"
	// RuntimeModePersistentPodWithInPodCron is reserved (not implemented).
	RuntimeModePersistentPodWithInPodCron RuntimeMode = "PersistentPodWithInPodCron"
)

// RuntimeSpec selects the Backup/Restore pod runtime.
type RuntimeSpec struct {
	// Mode is the pod runtime. Default CronJob.
	//
	// CronJob: existing behaviour — CronJob creates a Job pod per run; the PVC
	// is attached and detached every time.
	//
	// CronJobWithVolumeHolder: same CronJob pipeline, plus a 1-replica pause
	// Deployment that keeps the PVC attached. Job pods must schedule on that
	// node (required podAffinity). Persistence must stay enabled.
	//
	// PersistentPodWithTriggerJob: not implemented. Long-lived pipeline
	// Deployment (PVC stays mounted) plus a PVC-less trigger CronJob that
	// writes a run token and waits for a result ConfigMap.
	// Implementation notes: scratch must be $DATA_ROOT/run-$RUN_ID (HOSTNAME is
	// stable; already_done_hold would skip later runs). Stages must loop on the
	// token and must not exit after .step-job-done (Deployment restart would
	// skip work). Trigger Job needs a Role limited to the run/result ConfigMaps.
	// Idle memory is all stage requests; prefer CronJobWithVolumeHolder for
	// rare restores.
	//
	// PersistentPodWithInPodCron: not implemented. Same long-lived pipeline
	// pod, but a sidecar (e.g. supercronic) fires the run token. No CronJob;
	// kubectl create job --from=… goes away; last-run status cannot use Job
	// objects unless a result ConfigMap is added.
	// +kubebuilder:default=CronJob
	Mode RuntimeMode `json:"mode,omitempty"`
}

// EffectiveMode is CronJob when Runtime is nil or Mode is empty.
func (r *RuntimeSpec) EffectiveMode() RuntimeMode {
	if r == nil || r.Mode == "" {
		return RuntimeModeCronJob
	}
	return r.Mode
}

// JobPolicy maps onto CronJob / Job fields.
type JobPolicy struct {
	// ConcurrencyPolicy of the CronJob. Default Forbid.
	ConcurrencyPolicy string `json:"concurrencyPolicy,omitempty"`

	// FailedJobsHistoryLimit. Default 3.
	FailedJobsHistoryLimit *int32 `json:"failedJobsHistoryLimit,omitempty"`

	// SuccessfulJobsHistoryLimit. Default 3.
	SuccessfulJobsHistoryLimit *int32 `json:"successfulJobsHistoryLimit,omitempty"`

	// StartingDeadlineSeconds. Default 86400.
	StartingDeadlineSeconds *int64 `json:"startingDeadlineSeconds,omitempty"`

	// BackoffLimit on the Job. Default 3 for backup, 1 for restore.
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// TTLSecondsAfterFinished. Default 86400.
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// RestartPolicy of the Pod. Default Never (backup and restore).
	// OnFailure restarts one container in-place and can wipe pipeline markers.
	RestartPolicy corev1.RestartPolicy `json:"restartPolicy,omitempty"`

	// TimeZone for the CronJob schedule (Kubernetes 1.27+).
	TimeZone string `json:"timeZone,omitempty"`
}

const (
	LastJobOutcomeSucceeded = "Succeeded"
	LastJobOutcomeFailed    = "Failed"

	// ConditionReady is True when owned resources are admitted and synced.
	// It does not reflect whether the last Job succeeded.
	ConditionReady = "Ready"
	// ConditionBackupSucceeded is True when the last finished Backup Job succeeded.
	ConditionBackupSucceeded = "BackupSucceeded"
	// ConditionRestoreSucceeded is True when the last finished Restore Job succeeded.
	ConditionRestoreSucceeded = "RestoreSucceeded"
)

// LastJobStatus is the most recently finished Job for a Backup or Restore.
type LastJobStatus struct {
	// Name of the Job.
	Name string `json:"name,omitempty"`

	// Outcome is Succeeded or Failed.
	Outcome string `json:"outcome,omitempty"`

	// Reason from the JobComplete or JobFailed condition (e.g. BackoffLimitExceeded).
	Reason string `json:"reason,omitempty"`

	// Message from that condition.
	Message string `json:"message,omitempty"`

	// StartTime of the Job.
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime when the Job finished (or the condition transition time).
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
}
