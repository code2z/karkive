package config

// Config is operator-wide defaults, typically set from flags / Helm values.
type Config struct {
	BusyBoxImage  string
	GnuPGImage    string
	PostgresImage string
	// McImage is unused since 0.2.0 (kept so an old --mc-image flag still parses).
	McImage     string
	RcloneImage string
	// Stream runner image per engine (runner/<engine>/Dockerfile).
	RunnerPostgresImage string
	RunnerMariaDBImage  string
	RunnerRedisImage    string
	RunnerPVCImage      string
	MariaDBImage        string
	RedisImage          string
	TarImage            string

	DefaultS3Endpoint string
	DefaultS3Bucket   string
}

const (
	DefaultBusyBoxImage  = "docker.io/library/busybox:1.37"
	DefaultGnuPGImage    = "docker.io/vladgh/gpg:1.3.11"
	DefaultPostgresImage = "docker.io/cloudnative-pg/postgresql:18.4"
	DefaultMcImage       = "docker.io/minio/mc:RELEASE.2025-08-13T08-35-41Z"
	// rclone for Stage-mode s3-sync and fetch (official image: Alpine, /bin/sh).
	DefaultRcloneImage = "docker.io/rclone/rclone:1.76.0"
	// Single container of pipeline mode Stream, one image per engine (runner/<engine>/Dockerfile, tag in runner/<engine>/TAG).
	DefaultRunnerPostgresImage = "ghcr.io/mahdidarabi/karkive-runner-postgres:pg18.7-rclone1.76.0-r1"
	DefaultRunnerMariaDBImage  = "ghcr.io/mahdidarabi/karkive-runner-mariadb:mariadb10.6.23-rclone1.76.0-r1"
	DefaultRunnerRedisImage    = "ghcr.io/mahdidarabi/karkive-runner-redis:redis7.4.12-rclone1.76.0-r1"
	DefaultRunnerPVCImage      = "ghcr.io/mahdidarabi/karkive-runner-pvc:trixie-rclone1.76.0-r1"
	DefaultMariaDBImage        = "docker.io/library/mariadb:10.6"
	DefaultRedisImage          = "docker.io/library/redis:7.4"
	// GNU tar (pvcdump). BusyBox tar lacks --numeric-owner and large-file support.
	DefaultTarImage = "docker.io/library/debian:trixie-slim"

	DefaultDataDir     = "/backup/data"
	DefaultMcConfigDir = "/tmp/mc-config"
	DefaultWorkdir     = "/workdir"
	// DefaultPVCSourceDir is where engine pvc mounts the source claim (read-only).
	DefaultPVCSourceDir = "/source"

	// DefaultStreamWorkDir is the scratch mount of the Stream container (parallel dumps, seekable restore files, logs, retained/).
	DefaultStreamWorkDir = "/work"

	DefaultStreamBackupJobs  int32 = 1
	DefaultStreamRestoreJobs int32 = 2
	DefaultCompressionLevel  int32 = 3

	DefaultLocalRetentionDays int32 = 7
	DefaultS3RetentionDays    int32 = 14

	DefaultBackupSchedule  = "0 2 * * *"
	DefaultRestoreSchedule = "30 2 * * *"
)
