package resources

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
	"github.com/mahdidarabi/KArkive/internal/pipeline"
)

// IsStreamBackup is true when the Backup runs the single-container Stream pipeline.
func IsStreamBackup(backup *karkivev1alpha1.Backup) bool {
	return backup.Spec.Pipeline.IsStream()
}

// IsStreamRestore is true when the Restore runs the single-container Stream pipeline.
func IsStreamRestore(restore *karkivev1alpha1.Restore) bool {
	return restore.Spec.Pipeline.IsStream()
}

// streamBackupContainer is the only container of a Stream Backup: cleanup → dump | compress | encrypt | upload → prune.
// It mounts the data volume at /work (scratch for parallel dumps, retained/ when S3 is off) and, for engine pvc, the source claim read-only.
func streamBackupContainer(backup *karkivev1alpha1.Backup, cfg config.Config, cmName, secret string) corev1.Container {
	img, pull := runnerImage(backup.Spec.Images, cfg, backup.Spec.Engine)
	c := corev1.Container{
		Name:            "stream",
		Image:           img,
		ImagePullPolicy: pull,
		Command:         []string{"/bin/bash", "-c", pipeline.MustStreamScript("backup.sh")},
		EnvFrom: []corev1.EnvFromSource{{
			ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: cmName}},
		}},
		Resources:       backupStreamResources(backup),
		SecurityContext: streamBackupSecurityContext(backup),
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeDataDir, MountPath: config.DefaultStreamWorkDir},
			{Name: volumeTmp, MountPath: mountTmp, SubPath: "stream-tmp"},
			encryptionVolumeMount(backup.Spec.Encryption),
		},
	}
	switch EffectiveEngine(backup.Spec.Engine) {
	case karkivev1alpha1.EngineMariaDB:
		c.Env = append(c.Env, secretEnv("MYSQL_USER", secret, "username"), secretEnv("MYSQL_PASSWORD", secret, "password"))
	case karkivev1alpha1.EngineRedis:
		c.Env = append(c.Env, secretEnv("REDIS_USERNAME", secret, "username"), secretEnv("REDIS_PASSWORD", secret, "password"))
	case karkivev1alpha1.EnginePVC:
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: volumeSource, MountPath: config.DefaultPVCSourceDir, ReadOnly: true})
	default:
		c.Env = append(c.Env, secretEnv("PGUSER", secret, "username"), secretEnv("PGPASSWORD", secret, "password"))
	}
	if backup.Spec.S3.EnabledOrDefault() {
		c.Env = append(c.Env, secretEnv("S3_ACCESS_KEY", secret, "s3_access_key"), secretEnv("S3_SECRET_KEY", secret, "s3_secret_key"))
	}
	return c
}

// streamRestoreContainer is the only container of a Stream Restore: fetch | decrypt | load (pg_restore --jobs for Postgres).
func streamRestoreContainer(restore *karkivev1alpha1.Restore, cfg config.Config, cmName, secret, workdir string) corev1.Container {
	img, pull := runnerImage(restore.Spec.Images, cfg, restore.Spec.Engine)
	c := corev1.Container{
		Name:            "stream",
		Image:           img,
		ImagePullPolicy: pull,
		Command:         []string{"/bin/bash", "-c", pipeline.MustStreamScript("restore.sh")},
		EnvFrom: []corev1.EnvFromSource{{
			ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: cmName}},
		}},
		Resources:       restoreStreamResources(restore),
		SecurityContext: PostgresSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeWorkdir, MountPath: workdir},
			{Name: volumeTmp, MountPath: mountTmp, SubPath: "stream-tmp"},
			encryptionVolumeMount(restore.Spec.Encryption),
		},
		Env: []corev1.EnvVar{
			secretEnv("S3_ACCESS_KEY", secret, "s3_access_key"),
			secretEnv("S3_SECRET_KEY", secret, "s3_secret_key"),
		},
	}
	name, userKey, passKey := RestoreTargetSecret(restore)
	switch EffectiveEngine(restore.Spec.Engine) {
	case karkivev1alpha1.EngineMariaDB:
		c.Env = append(c.Env, secretEnv("MYSQL_USER", name, userKey), secretEnv("MYSQL_PASSWORD", name, passKey))
	case karkivev1alpha1.EngineRedis:
		c.Env = append(c.Env, optionalSecretEnv("REDIS_USERNAME", name, userKey), secretEnv("REDIS_PASSWORD", name, passKey))
	default:
		c.Env = append(c.Env, secretEnv("PGUSER", name, userKey), secretEnv("PGPASSWORD", name, passKey))
	}
	return c
}

// optionalSecretEnv is secretEnv for a key that may be absent (Redis ACL username).
func optionalSecretEnv(envName, secret, key string) corev1.EnvVar {
	e := secretEnv(envName, secret, key)
	opt := true
	e.ValueFrom.SecretKeyRef.Optional = &opt
	return e
}

// streamBackupSecurityContext runs as the runner user (UID 26). Engine pvc keeps the tar rules of Stage mode (root with only CAP_DAC_OVERRIDE, or spec.pvc.runAsUser).
func streamBackupSecurityContext(backup *karkivev1alpha1.Backup) *corev1.SecurityContext {
	if IsPVCBackup(backup) {
		return PVCDumpSecurityContext(backup.Spec.PVC)
	}
	return PostgresSecurityContext()
}

// streamBackupEnv is merged into the Backup ConfigMap in Stream mode.
func streamBackupEnv(backup *karkivev1alpha1.Backup) map[string]string {
	env := map[string]string{
		"PIPELINE_MODE":     string(karkivev1alpha1.PipelineModeStream),
		"HOME":              mountTmp,
		"WORK_ROOT":         config.DefaultStreamWorkDir,
		"JOBS":              strconv.Itoa(int(pipelineJobs(backup.Spec.Pipeline, config.DefaultStreamBackupJobs))),
		"COMPRESSION_LEVEL": strconv.Itoa(int(compressionLevel(backup.Spec.Pipeline))),
	}
	if backup.Spec.Encryption.IsAge() {
		env["AGE_RECIPIENTS_FILE"] = mountAge + "/" + SecretKeyAgeRecipient
	} else {
		env["GPG_PASSFILE"] = mountGPG + "/" + SecretKeyGPGPassphrase
	}
	return env
}

// streamRestoreEnv is merged into the Restore ConfigMap in Stream mode.
func streamRestoreEnv(restore *karkivev1alpha1.Restore) map[string]string {
	env := map[string]string{
		"PIPELINE_MODE": string(karkivev1alpha1.PipelineModeStream),
		"HOME":          mountTmp,
		"WORK_ROOT":     restoreWorkdir(restore),
		"JOBS":          strconv.Itoa(int(pipelineJobs(restore.Spec.Pipeline, config.DefaultStreamRestoreJobs))),
	}
	// An emptyDir reports the node's free disk to df, not its sizeLimit: the script also checks against this size.
	if limit := scratchLimitBytes(restore.Spec.Persistence); limit != "" {
		env["SCRATCH_LIMIT_BYTES"] = limit
	}
	if restore.Spec.Encryption.IsAge() {
		env["AGE_IDENTITY_FILE"] = mountAge + "/" + SecretKeyAgeIdentity
	} else {
		env["GPG_PASSFILE"] = mountGPG + "/" + SecretKeyGPGPassphrase
	}
	return env
}
