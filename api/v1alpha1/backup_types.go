package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupResources overrides CPU/memory/ephemeral-storage per pipeline stage.
type BackupResources struct {
	Cleanup *corev1.ResourceRequirements `json:"cleanup,omitempty"`
	// Dump is pgdump, mysqldump, redisdump, or pvcdump (tar).
	Dump     *corev1.ResourceRequirements `json:"dump,omitempty"`
	Compress *corev1.ResourceRequirements `json:"compress,omitempty"`
	Encrypt  *corev1.ResourceRequirements `json:"encrypt,omitempty"`
	S3Sync   *corev1.ResourceRequirements `json:"s3Sync,omitempty"`
}

// PVCSourceSpec is the PersistentVolumeClaim that engine pvc archives with tar.
type PVCSourceSpec struct {
	// ClaimName of the source PVC. It must be in the Backup's namespace.
	// The claim is mounted read-only, and only into the pvcdump container.
	// ReadWriteOncePod claims cannot be mounted next to their consumer.
	// +kubebuilder:validation:MinLength=1
	ClaimName string `json:"claimName"`

	// Path inside the claim to archive, relative to its root (no leading /
	// and no .. segments). Default: the whole claim.
	Path string `json:"path,omitempty"`

	// Excludes are GNU tar --exclude patterns matched against member names
	// such as ./cache/file. A bare name (cache) matches at any depth;
	// ./cache matches only the top-level entry.
	Excludes []string `json:"excludes,omitempty"`

	// ConsumerSelector matches the pods that already mount the claim. Job
	// pods (and the volume holder) then get required podAffinity to them on
	// kubernetes.io/hostname, so a ReadWriteOnce claim is mounted on the node
	// it is attached to. Leave unset for RWX claims or claims with no running
	// consumer. While set, Jobs stay Pending if no matching pod is running.
	ConsumerSelector *metav1.LabelSelector `json:"consumerSelector,omitempty"`

	// RunAsUser of the pvcdump container. Unset (or 0) runs tar as root with
	// only CAP_DAC_OVERRIDE, so it can read files of any owner and mode; the
	// source mount is read-only. Set a non-root UID (for example the app's)
	// where Pod Security "restricted" forbids root; tar then reads only what
	// that UID can read, and unreadable files fail the backup.
	// +kubebuilder:validation:Minimum=0
	RunAsUser *int64 `json:"runAsUser,omitempty"`

	// RunAsGroup of the pvcdump container. Defaults to RunAsUser.
	// +kubebuilder:validation:Minimum=0
	RunAsGroup *int64 `json:"runAsGroup,omitempty"`
}

// BackupSpec defines the desired state of Backup.
type BackupSpec struct {
	// Engine of the source datastore.
	// +kubebuilder:default=postgres
	Engine Engine `json:"engine,omitempty"`

	// Schedule is a standard Cron expression (required).
	// +kubebuilder:validation:MinLength=1
	Schedule string `json:"schedule"`

	// Suspend stops scheduled runs. The CronJob remains for
	// `kubectl create job --from=cronjob/karkive-backup-<name>`.
	Suspend *bool `json:"suspend,omitempty"`

	// Database connection (non-secret fields). Required for postgres, mariadb,
	// and redis; unused for pvc.
	Database DatabaseSpec `json:"database,omitempty"`

	// PVC is the claim to archive. Required for engine pvc; rejected otherwise.
	PVC *PVCSourceSpec `json:"pvc,omitempty"`

	// S3 destination for encrypted dumps. Set enabled=false to keep dumps only
	// in retained/ on the PVC (no s3-sync; S3 keys and endpoint/bucket not required).
	S3 S3Spec `json:"s3"`

	// SecretRef is a Secret in the same namespace with keys: gpg_passphrase;
	// username and password (not for engine pvc); and (when s3.enabled)
	// s3_access_key, s3_secret_key.
	SecretRef corev1.LocalObjectReference `json:"secretRef"`

	// Persistence for dump scratch + retained/. Default: PVC enabled, 1Gi.
	Persistence *PersistenceSpec `json:"persistence,omitempty"`

	// LocalRetentionDays for retained/ encrypted dumps on the PVC. Default 7.
	// +kubebuilder:validation:Minimum=1
	LocalRetentionDays *int32 `json:"localRetentionDays,omitempty"`

	// LogFileEnabled writes stage logs to logs/<pod>.log on the volume as well
	// as stderr. Default false. Files older than LocalRetentionDays are pruned.
	LogFileEnabled *bool `json:"logFileEnabled,omitempty"`

	// DataDir is the volume mount path. Default /backup/data.
	DataDir string `json:"dataDir,omitempty"`

	// McConfigDir for the minio client. Default /tmp/mc-config.
	McConfigDir string `json:"mcConfigDir,omitempty"`

	// Images overrides operator-wide default images.
	Images *ImageSet `json:"images,omitempty"`

	// Resources overrides per-stage resource requests/limits.
	Resources *BackupResources `json:"resources,omitempty"`

	// Job tunes CronJob/Job behaviour.
	Job *JobPolicy `json:"job,omitempty"`

	// Runtime selects how pipeline pods are scheduled relative to the PVC.
	// Default mode CronJob. CronJobWithVolumeHolder is implemented.
	// PersistentPodWithTriggerJob and PersistentPodWithInPodCron are rejected
	// until implemented.
	Runtime *RuntimeSpec `json:"runtime,omitempty"`

	// Component is the app.kubernetes.io/component label. Defaults to metadata.name.
	Component string `json:"component,omitempty"`
}

const (
	BackupPhasePending     = "Pending"
	BackupPhaseReady       = "Ready"
	BackupPhaseError       = "Error"
	BackupPhaseUnsupported = "Unsupported"
)

// BackupStatus defines the observed state of Backup.
type BackupStatus struct {
	// Phase is admission of owned resources: Pending, Ready, Error, Unsupported.
	// It is not the last Job outcome; see BackupSucceeded.
	Phase string `json:"phase,omitempty"`

	// ObservedGeneration is the spec generation last processed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// CronJobName is the owned CronJob.
	CronJobName string `json:"cronJobName,omitempty"`

	// VolumeHolderName is the pause Deployment that keeps the PVC attached.
	// Empty unless spec.runtime.mode is CronJobWithVolumeHolder.
	VolumeHolderName string `json:"volumeHolderName,omitempty"`

	// LastScheduleTime copied from the CronJob.
	LastScheduleTime *metav1.Time `json:"lastScheduleTime,omitempty"`

	// LastSuccessfulTime copied from the CronJob.
	LastSuccessfulTime *metav1.Time `json:"lastSuccessfulTime,omitempty"`

	// LastJob is the most recently finished Job, including failure reason.
	LastJob *LastJobStatus `json:"lastJob,omitempty"`

	// Conditions of the Backup. Ready is admission/sync of owned resources.
	// BackupSucceeded is the last finished Job (Unknown until one exists).
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=kbackup;bak
// +kubebuilder:metadata:annotations="helm.sh/resource-policy=keep"
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=".spec.engine"
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=".spec.schedule"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Succeeded",type=string,JSONPath=".status.conditions[?(@.type=='BackupSucceeded')].status"
// +kubebuilder:printcolumn:name="Last Success",type=date,JSONPath=".status.lastSuccessfulTime"
// +kubebuilder:printcolumn:name="Last Job",type=string,JSONPath=".status.lastJob.outcome"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Backup describes a scheduled backup pipeline (database dump or PVC tar → gzip → gpg → optional S3).
type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupSpec   `json:"spec,omitempty"`
	Status BackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BackupList contains a list of Backup.
type BackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Backup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Backup{}, &BackupList{})
}
