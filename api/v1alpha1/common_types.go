package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Engine identifies the datastore being backed up or restored.
// pvc archives a PersistentVolumeClaim's files with tar (Backup only).
// +kubebuilder:validation:Enum=postgres;mariadb;redis;pvc
type Engine string

const (
	EnginePostgres Engine = "postgres"
	EngineMariaDB  Engine = "mariadb"
	EngineRedis    Engine = "redis"
	EnginePVC      Engine = "pvc"
)

// DatabaseSpec is the connection target for dump or restore.
type DatabaseSpec struct {
	// Host is the DNS name or IP of the database. Required for postgres,
	// mariadb, and redis (checked by the webhook and controller).
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host,omitempty"`

	// Port of the database. Defaults: postgres 5432, mariadb 3306, redis 6379.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`

	// Name is the database name (postgres/mariadb) or a logical label (redis).
	// Required for postgres, mariadb, and redis.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`

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
	// Raising it expands the existing PVC (the StorageClass must set
	// allowVolumeExpansion); lowering it is ignored because PVCs cannot shrink.
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
	// GnuPG is used for encrypt and decrypt with encryption method gpg (Stage mode).
	GnuPG *ImageSpec `json:"gnupg,omitempty"`
	// Age is used for encrypt and decrypt with encryption method age (Stage mode). It needs /bin/sh and age.
	Age *ImageSpec `json:"age,omitempty"`
	// Postgres is used for pg_dump / pg_restore / psql.
	Postgres *ImageSpec `json:"postgres,omitempty"`
	// Mc is deprecated and ignored since 0.2.0: Stage mode uses Rclone for s3-sync and fetch.
	Mc *ImageSpec `json:"mc,omitempty"`
	// Rclone is used for s3-sync and fetch in Stage mode. It needs /bin/sh and rclone.
	Rclone *ImageSpec `json:"rclone,omitempty"`
	// MariaDB is used for mysqldump / mysql restore.
	MariaDB *ImageSpec `json:"mariadb,omitempty"`
	// Redis is used for redis-cli dump and restore.
	Redis *ImageSpec `json:"redis,omitempty"`
	// Tar must provide GNU tar; used by the pvcdump stage (engine pvc).
	Tar *ImageSpec `json:"tar,omitempty"`
	// Runner is the single container of pipeline mode Stream; the default is the per-engine karkive-runner-<engine> image. It needs bash, the engine's client tools (pg_dump/pg_restore, mariadb-dump, redis-cli, or GNU tar), zstd, gpg, age, and rclone.
	Runner *ImageSpec `json:"runner,omitempty"`
}

// EncryptionMethod selects how dumps are encrypted at rest.
// +kubebuilder:validation:Enum=gpg;age
type EncryptionMethod string

const (
	// EncryptionMethodGPG is symmetric gpg (AES256) with the gpg_passphrase Secret key. Objects end in .gpg. Default.
	EncryptionMethodGPG EncryptionMethod = "gpg"
	// EncryptionMethodAge is age (X25519). A Backup encrypts to the public key(s) in age_recipient; a Restore decrypts with the private key in age_identity. Objects end in .age.
	EncryptionMethodAge EncryptionMethod = "age"
)

// EncryptionSpec selects the encryption of dumps and archives.
type EncryptionSpec struct {
	// Method is gpg (default, symmetric, Secret key gpg_passphrase) or age
	// (public key). With age a Backup reads age_recipient (one or more age1...
	// public keys, one per line) and a Restore reads age_identity (the
	// AGE-SECRET-KEY-1... private key) from spec.secretRef; gpg_passphrase is
	// then not required. age objects end in .age instead of .gpg, and a Restore
	// only picks objects of its own method.
	// +kubebuilder:default=gpg
	Method EncryptionMethod `json:"method,omitempty"`
}

// EffectiveMethod is gpg when the spec or its method is unset.
func (e *EncryptionSpec) EffectiveMethod() EncryptionMethod {
	if e == nil || e.Method == "" {
		return EncryptionMethodGPG
	}
	return e.Method
}

// IsAge is true for encryption method age.
func (e *EncryptionSpec) IsAge() bool {
	return e.EffectiveMethod() == EncryptionMethodAge
}

// Extension is the object suffix of the method without the dot: gpg or age.
func (e *EncryptionSpec) Extension() string {
	return string(e.EffectiveMethod())
}

// PipelineMode selects how the pipeline runs inside the Job pod.
// +kubebuilder:validation:Enum=Stage;Stream
type PipelineMode string

const (
	// PipelineModeStage runs one container per stage (cleanup, dump, compress, encrypt, s3-sync; restore: cleanup, fetch, decrypt, extract, restore), handing files over the shared volume.
	PipelineModeStage PipelineMode = "Stage"
	// PipelineModeStream runs one container. Backup streams dump | compress | encrypt | upload with no plain file on disk; Postgres uses pg_dump's custom (or directory) format with zstd, so restores load with pg_restore --jobs. Default.
	PipelineModeStream PipelineMode = "Stream"
)

// PipelineSpec tunes the pipeline. All fields are optional.
type PipelineSpec struct {
	// Mode is Stream (default) or Stage.
	// Stream objects are named *.dump.gpg / *.dir.tar.gpg (postgres) and *.zst.gpg (other engines); with encryption method age the suffix is .age instead of .gpg. A Stream Restore also reads Stage objects (*.gz.gpg / *.gz.age); a Stage Restore reads only Stage objects.
	// +kubebuilder:default=Stream
	Mode PipelineMode `json:"mode,omitempty"`

	// Jobs is the parallelism of the database tools in Stream mode: pg_dump --jobs (directory format, scratch holds the compressed dump), pg_restore --jobs and vacuumdb --jobs. Default 1 for Backup and 2 for Restore. Ignored in Stage mode.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=32
	Jobs *int32 `json:"jobs,omitempty"`

	// CompressionLevel is the zstd level in Stream mode (pg_dump --compress=zstd:N, otherwise zstd -N). Default 3. Ignored in Stage mode (gzip).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=19
	CompressionLevel *int32 `json:"compressionLevel,omitempty"`
}

// EffectiveMode is Stream when the spec or its mode is unset.
func (p *PipelineSpec) EffectiveMode() PipelineMode {
	if p == nil || p.Mode == "" {
		return PipelineModeStream
	}
	return p.Mode
}

// IsStream is true for pipeline mode Stream.
func (p *PipelineSpec) IsStream() bool {
	return p.EffectiveMode() == PipelineModeStream
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
