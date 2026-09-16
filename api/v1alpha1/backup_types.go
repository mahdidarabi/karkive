package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupResources overrides CPU/memory/ephemeral-storage per pipeline stage.
type BackupResources struct {
	Cleanup  *corev1.ResourceRequirements `json:"cleanup,omitempty"`
	Dump     *corev1.ResourceRequirements `json:"dump,omitempty"`
	Compress *corev1.ResourceRequirements `json:"compress,omitempty"`
	Encrypt  *corev1.ResourceRequirements `json:"encrypt,omitempty"`
	S3Sync   *corev1.ResourceRequirements `json:"s3Sync,omitempty"`
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

	// Database connection (non-secret fields).
	Database DatabaseSpec `json:"database"`

	// S3 destination for encrypted dumps. Set enabled=false to keep dumps only
	// in retained/ on the PVC (no s3-sync; S3 keys and endpoint/bucket not required).
	S3 S3Spec `json:"s3"`

	// SecretRef is a Secret in the same namespace with keys:
	// username, password, gpg_passphrase, and (when s3.enabled) s3_access_key, s3_secret_key.
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

// Backup describes a scheduled logical backup pipeline (dump → gzip → gpg → optional S3).
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
