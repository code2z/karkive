set -eu
umask 0002
STAGE=fetch
# Shared PVC is reused across Jobs; scope scratch to this pod
# so peers never see stale .step-* markers from a prior run.
WORKDIR_ROOT="${WORKDIR}"
WORKDIR="${WORKDIR_ROOT}/${HOSTNAME}"
pipeline_init
fallback_enabled() {
  case "${USE_LATEST_BACKUP_AS_FALLBACK:-}" in
    yes|YES|true|TRUE|1) return 0 ;;
    *) return 1 ;;
  esac
}
find_latest() {
  DUMP_PREFIX="${DUMP_PREFIX:-pg_dump}"
  case "${DUMP_PREFIX}" in
    mysqldump) S3_NAME='mysqldump-*.sql.gz.gpg' ;;
    redisdump) S3_NAME='redisdump-*.rdb.gz.gpg' ;;
    *)         S3_NAME='pg_dump-*.pgdump.gz.gpg' ;;
  esac
  log "finding latest ${S3_NAME} under ${PREFIX}/"
  # Newest by upload time (S3 LastModified), not by name.
  LATEST="$(rclone lsf --files-only --format tp --separator '|' --include "${S3_NAME}" "${PREFIX}/" \
    | sort | tail -n 1 | cut -d'|' -f2-)"
  if [ -z "${LATEST}" ]; then
    log "ERROR: no ${S3_NAME} under ${PREFIX}/" >&2
    mark_failed
    exit 1
  fi
  log "selected latest backup: ${LATEST}"
  printf '%s' "${PREFIX}/${LATEST}"
}
wait_for "${WORKDIR}/.step-cleanup-done" "cleanup"
already_done_hold "${WORKDIR}/.step-fetch-done" "fetch"
clear_step_failed
log "stage start: fetch bucket=${S3_BUCKET} path=${S3_PATH}"
log "scratch dir=${WORKDIR}"
log "clearing workdir dumps under ${WORKDIR} (keep .step-cleanup-done)"
rm -f "${WORKDIR}/dump"*
log "configuring rclone endpoint=${S3_ENDPOINT}"
rclone_s3_init
PREFIX="${S3_PREFIX}"
if [ -n "${BACKUP_FILE:-}" ]; then
  SRC="${PREFIX}/${BACKUP_FILE}"
  log "checking explicit BACKUP_FILE=${BACKUP_FILE}"
  if [ -n "$(rclone lsf --files-only "${SRC}" 2>/dev/null)" ]; then
    log "BACKUP_FILE exists: ${SRC}"
  else
    log "BACKUP_FILE not found: ${SRC}"
    if fallback_enabled; then
      log "USE_LATEST_BACKUP_AS_FALLBACK=${USE_LATEST_BACKUP_AS_FALLBACK}; falling back to latest"
      SRC="$(find_latest)"
    else
      log "ERROR: BACKUP_FILE missing and USE_LATEST_BACKUP_AS_FALLBACK is not yes" >&2
      mark_failed
      exit 1
    fi
  fi
else
  if fallback_enabled; then
    log "BACKUP_FILE empty; USE_LATEST_BACKUP_AS_FALLBACK=${USE_LATEST_BACKUP_AS_FALLBACK}"
    SRC="$(find_latest)"
  else
    log "ERROR: BACKUP_FILE is empty; refusing to pick latest without agreement" >&2
    log "ERROR: set BACKUP_FILE=<object> OR USE_LATEST_BACKUP_AS_FALLBACK=yes" >&2
    mark_failed
    exit 1
  fi
fi
log "fetching ${SRC}"
rclone copyto "${SRC}" "${WORKDIR}/dump.gz.gpg"
log "fetched size=$(wc -c < "${WORKDIR}/dump.gz.gpg") bytes"
touch "${WORKDIR}/.step-fetch-done"
log "wrote marker .step-fetch-done; stage work done"
hold_until_job_done
