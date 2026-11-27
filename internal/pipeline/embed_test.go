package pipeline

import (
	"strings"
	"testing"
)

func TestCommonScriptEmbedded(t *testing.T) {
	s := CommonScript()
	if s == "" {
		t.Fatal("common.sh is empty")
	}
	for _, fn := range []string{"log()", "wait_for()", "hold_until_job_done()", "mark_failed()", "pipeline_init()", "log_file_enabled()", "prune_pipeline_logs()", "already_done_hold()", "already_done_exit()", "clear_step_failed()", "log_file_lines()", "dump_heartbeat_start()", "dump_heartbeat_stop()"} {
		if !strings.Contains(s, fn) {
			t.Errorf("common.sh missing %s", fn)
		}
	}
	if !strings.Contains(s, "LOG_FILE_ENABLED") {
		t.Error("common.sh should append stage logs to LOG_FILE when LOG_FILE_ENABLED is set")
	}
}

func TestBackupScriptsEmbedded(t *testing.T) {
	for _, name := range []string{"cleanup.sh", "pgdump.sh", "mysqldump.sh", "redisdump.sh", "pvcdump.sh", "compress.sh", "encrypt.sh", "s3-sync.sh"} {
		s, err := BackupScript(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s == "" {
			t.Fatalf("%s is empty", name)
		}
		assertComposedOnce(t, name, s)
	}
}

func TestRestoreScriptsEmbedded(t *testing.T) {
	for _, name := range []string{"cleanup.sh", "fetch.sh", "decrypt.sh", "extract.sh", "pgrestore.sh", "mysqlrestore.sh", "redisrestore.sh"} {
		s, err := RestoreScript(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s == "" {
			t.Fatalf("%s is empty", name)
		}
		assertComposedOnce(t, name, s)
	}
}

func assertComposedOnce(t *testing.T, name, s string) {
	t.Helper()
	if !strings.HasPrefix(s, CommonScript()) {
		t.Errorf("%s: expected common.sh to be prepended", name)
	}
	for _, fn := range []string{"log() {", "wait_for() {", "hold_until_job_done() {", "mark_failed() {"} {
		if n := strings.Count(s, fn); n != 1 {
			t.Errorf("%s: want one %q, got %d", name, fn, n)
		}
	}
}

func TestMariaDBDumpRestoreFlags(t *testing.T) {
	dump, err := BackupScript("mysqldump.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--hex-blob", "--default-character-set=utf8mb4", "--databases", "--max-statement-time=0"} {
		if !strings.Contains(dump, want) {
			t.Errorf("mysqldump.sh missing %q", want)
		}
	}
	restore, err := RestoreScript("mysqlrestore.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GTID_PURGED", "DEFINER=CURRENT_USER", "utf8mb4_unicode_ci", "unique_checks=0"} {
		if !strings.Contains(restore, want) {
			t.Errorf("mysqlrestore.sh missing %q", want)
		}
	}
}

func TestRedisRestoreUsesReplicaOf(t *testing.T) {
	s, err := RestoreScript("redisrestore.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "REPLICAOF") {
		t.Fatal("redisrestore.sh should use REPLICAOF")
	}
	if strings.Contains(s, "MIGRATE") {
		t.Fatal("redisrestore.sh should not SCAN+MIGRATE keys")
	}
	if strings.Contains(s, "FLUSHALL") {
		t.Fatal("redisrestore.sh should not FLUSHALL; REPLICAOF replaces the dataset")
	}
}

func TestCleanupKeepsLogsDir(t *testing.T) {
	backup, err := BackupScript("cleanup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(backup, "! -name logs") {
		t.Error("backup cleanup.sh must not delete the logs/ directory")
	}
	if !strings.Contains(backup, "prune_pipeline_logs") {
		t.Error("backup cleanup.sh should prune expired pipeline logs")
	}
	restore, err := RestoreScript("cleanup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restore, "! -name logs") {
		t.Error("restore cleanup.sh must not delete the logs/ directory")
	}
}

func TestEncryptHonorsS3Enabled(t *testing.T) {
	s, err := BackupScript("encrypt.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "S3_ENABLED") {
		t.Fatal("encrypt.sh should honor S3_ENABLED")
	}
	if !strings.Contains(s, ".step-job-done") {
		t.Fatal("encrypt.sh should write .step-job-done when S3 is disabled")
	}
}

func TestPgRestoreStripsPgAuditEventTriggers(t *testing.T) {
	s, err := RestoreScript("pgrestore.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"strip_pgaudit_ddl",
		"CREATE EVENT TRIGGER",
		"pgaudit_(ddl_command_end|sql_drop)",
		"GRANT[[:space:]].*ON FUNCTION",
		"REVOKE[[:space:]].*ON FUNCTION",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pgrestore.sh missing %q", want)
		}
	}
}

func TestPgRestoreDetectsExtensionsFromDDLOnly(t *testing.T) {
	s, err := RestoreScript("pgrestore.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`grep -Eq '^(CREATE|DROP) EXTENSION([[:space:]]+IF([[:space:]]+NOT)?[[:space:]]+EXISTS)?[[:space:]]+("timescaledb"|timescaledb)'`,
		`grep -Eq '^COPY[[:space:]]+"?_timescaledb_catalog"?\.'`,
		`grep -Eq '^(CREATE|DROP) EXTENSION([[:space:]]+IF([[:space:]]+NOT)?[[:space:]]+EXISTS)?[[:space:]]+("pgaudit"|pgaudit)'`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pgrestore.sh missing %q", want)
		}
	}
	// Unanchored patterns also match COPY data that mentions the extension.
	for _, bad := range []string{
		`grep -Eq 'CREATE EXTENSION.*`,
		`grep -Eq 'DROP EXTENSION.*`,
		`grep -Eq '_timescaledb_catalog'`,
	} {
		if strings.Contains(s, bad) {
			t.Errorf("pgrestore.sh has unanchored detection %q", bad)
		}
	}
}

func TestPgDumpDisablesStatementTimeout(t *testing.T) {
	s, err := BackupScript("pgdump.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "statement_timeout=0") {
		t.Fatal("pgdump.sh should disable statement_timeout for long dumps")
	}
	if !strings.Contains(s, "dump_heartbeat_start") {
		t.Fatal("pgdump.sh should heartbeat while pg_dump runs")
	}
}

func TestPVCDumpScript(t *testing.T) {
	s, err := BackupScript("pvcdump.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"STAGE=pvcdump",
		"PVC_CLAIM_NAME",
		"--numeric-owner",
		"--exclude-from=",
		`[ "$ec" -eq 1 ]`,
		"pvcdump-${CLAIM}-",
		".step-dump-done",
		"dump_heartbeat_start",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pvcdump.sh missing %q", want)
		}
	}
}

func TestBackupStagesKnowPVCDumpPrefix(t *testing.T) {
	for name, glob := range map[string]string{
		"cleanup.sh":  "pvcdump-*.tar.gz.gpg",
		"compress.sh": "pvcdump-*.tar'",
		"encrypt.sh":  "pvcdump-*.tar.gz'",
		"s3-sync.sh":  "pvcdump-*.tar.gz'",
	} {
		s, err := BackupScript(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s, glob) {
			t.Errorf("%s missing pvcdump glob %q", name, glob)
		}
	}
}

func TestS3SyncUsesRcloneAndPrunesBestEffort(t *testing.T) {
	s, err := BackupScript("s3-sync.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rclone_s3_init", "rclone copy", "rclone delete", "--min-age", "|| true"} {
		if !strings.Contains(s, want) {
			t.Errorf("s3-sync.sh missing %q", want)
		}
	}
	if strings.Contains(s, "mc ") {
		t.Error("s3-sync.sh still calls mc")
	}
	f, err := RestoreScript("fetch.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f, "mc ") || !strings.Contains(f, "rclone copyto") {
		t.Error("fetch.sh must fetch with rclone, not mc")
	}
}

func TestStreamScripts(t *testing.T) {
	for name, wants := range map[string][]string{
		"backup.sh": {
			"set -Eeuo pipefail",
			"stream_init",
			"--format=custom --compress=\"zstd:${LVL}\"",
			"--format=directory --jobs=",
			"run_dump | encrypt_stream | rclone rcat",
			"s3_prune",
			"--cipher-algo AES256 --compress-algo none",
		},
		"restore.sh": {
			"rclone cat",
			"pg_restore",
			"--jobs=",
			"timescaledb_pre_restore",
			"decompress_stream",
			"REDIS_RESTORE_METHOD",
			"redis_pull_in_target",
			"redis_push_to_target",
			"need_scratch",
			"SCRATCH_LIMIT_BYTES",
			"COPY",
			"REPLACE",
		},
	} {
		s, err := StreamScript(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(s, want) {
				t.Errorf("stream %s missing %q", name, want)
			}
		}
	}
}

func TestEncryptionMethodDefaultsToGPG(t *testing.T) {
	c := CommonScript()
	for _, want := range []string{`ENCRYPTION_METHOD="${ENCRYPTION_METHOD:-gpg}"`, `ENC_EXT="${ENCRYPTION_METHOD}"`} {
		if !strings.Contains(c, want) {
			t.Errorf("common.sh missing %q", want)
		}
	}
	s, err := StreamScript("backup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `ENCRYPTION_METHOD="${ENCRYPTION_METHOD:-gpg}"`) {
		t.Error("stream common.sh must default ENCRYPTION_METHOD to gpg")
	}
}

func TestStageScriptsSupportAge(t *testing.T) {
	enc, err := BackupScript("encrypt.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// gpg path unchanged
		"gpg --batch --yes --symmetric --cipher-algo AES256",
		"/run/secrets/gpg/gpg_passphrase",
		// age path: public key only
		`age --encrypt --recipients-file "${AGE_RECIPIENTS_FILE}"`,
		"/run/secrets/age/age_recipient",
		`out="${f}.${ENC_EXT}"`,
	} {
		if !strings.Contains(enc, want) {
			t.Errorf("encrypt.sh missing %q", want)
		}
	}
	dec, err := RestoreScript("decrypt.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"gpg --batch --yes --decrypt",
		`age --decrypt --identity "${AGE_IDENTITY_FILE}"`,
		"/run/secrets/age/age_identity",
		`IN="${WORKDIR}/dump.gz.${ENC_EXT}"`,
	} {
		if !strings.Contains(dec, want) {
			t.Errorf("decrypt.sh missing %q", want)
		}
	}
	fetch, err := RestoreScript("fetch.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`S3_NAME="pg_dump-*.pgdump.gz.${ENC_EXT}"`,
		`S3_NAME="mysqldump-*.sql.gz.${ENC_EXT}"`,
		`S3_NAME="redisdump-*.rdb.gz.${ENC_EXT}"`,
		"gpg:*.age|age:*.gpg",
		`"${WORKDIR}/dump.gz.${ENC_EXT}"`,
	} {
		if !strings.Contains(fetch, want) {
			t.Errorf("fetch.sh missing %q", want)
		}
	}
	// Retention prunes both suffixes, so switching methods still ages out older dumps.
	for name, wants := range map[string][]string{
		"cleanup.sh": {`-name "${RETAINED_GLOB}.gpg" -o -name "${RETAINED_GLOB}.age"`, "'pvcdump-*.tar.gz.age'"},
		"s3-sync.sh": {`--include "${S3_NAME}.gpg" --include "${S3_NAME}.age"`},
	} {
		s, err := BackupScript(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(s, want) {
				t.Errorf("%s missing %q", name, want)
			}
		}
	}
}

func TestStreamScriptsSupportAge(t *testing.T) {
	b, err := StreamScript("backup.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`age --encrypt --recipients-file "${AGE_RECIPIENTS_FILE}"`,
		"--symmetric --cipher-algo AES256 --compress-algo none",
		`NAME="pg_dump-${PGDATABASE}-${TS}.dump.${ENC_EXT}"`,
		`NAME="pg_dump-${PGDATABASE}-${TS}.dir.tar.${ENC_EXT}"`,
		`NAME="mysqldump-${MYSQL_DATABASE}-${TS}.sql.zst.${ENC_EXT}"`,
		`NAME="redisdump-${REDIS_NAME:-redis}-${TS}.rdb.zst.${ENC_EXT}"`,
		`NAME="pvcdump-${PVC_CLAIM_NAME}-${TS}.tar.zst.${ENC_EXT}"`,
		`BASE="${NAME%."${ENC_EXT}"}"`,
		`"${S3_PREFIX}/${BASE}.meta"`,
		`s3_prune "${INCLUDE}"`,
		"-name '*.age'",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("stream backup.sh missing %q", want)
		}
	}
	if strings.Contains(b, "gpg_encrypt") {
		t.Error("stream backup.sh still calls gpg_encrypt directly")
	}
	r, err := StreamScript("restore.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`age --decrypt --identity "${AGE_IDENTITY_FILE}"`,
		`rclone cat "${SRC}" | decrypt_stream`,
		`INCLUDE="pg_dump-*.${ENC_EXT}"`,
		`INCLUDE="mysqldump-*.${ENC_EXT}"`,
		`INCLUDE="redisdump-*.${ENC_EXT}"`,
		"*.dump.age|*.dir.tar.age",
		`${OBJ%."${ENC_EXT}"}.meta`,
		"*.zst.gpg|*.zst.age",
		"*.gz.gpg|*.gz.age",
		"gpg:*.age|age:*.gpg",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("stream restore.sh missing %q", want)
		}
	}
}
