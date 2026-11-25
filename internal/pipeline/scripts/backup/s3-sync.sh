set -eu
umask 0002
STAGE=s3-sync
# Shared PVC is reused across Jobs; scope scratch to this pod
# so peers never see stale .step-* markers from a prior run.
DATA_ROOT="${DATA_DIR:-${PGDUMP_DIR:?DATA_DIR or PGDUMP_DIR required}}"
DATA_DIR="${DATA_ROOT}/${HOSTNAME}"
pipeline_init
wait_for "${DATA_DIR}/.step-encrypt-done" "encrypt"
already_done_exit "${DATA_DIR}/.step-job-done" "s3-sync"
log "stage start: s3-sync bucket=${S3_BUCKET} path=${S3_PATH}"
log "configuring rclone endpoint=${S3_ENDPOINT}"
rclone_s3_init
log "uploading ${DATA_DIR}/ -> ${S3_PREFIX}/"
rclone copy --exclude '.step-*' --exclude '.*' "${DATA_DIR}/" "${S3_PREFIX}/"
# Prefer S3_RETENTION_DAYS; fall back to S3_RETENTION_HOURS (legacy).
if [ -n "${S3_RETENTION_DAYS:-}" ]; then
  S3_OLDER_THAN="$((S3_RETENTION_DAYS * 24))h"
else
  S3_OLDER_THAN="${S3_RETENTION_HOURS:-48}h"
fi
DUMP_PREFIX="${DUMP_PREFIX:-pg_dump}"
case "${DUMP_PREFIX}" in
  mysqldump) S3_NAME='mysqldump-*.sql.gz.gpg' ;;
  redisdump) S3_NAME='redisdump-*.rdb.gz.gpg' ;;
  pvcdump)   S3_NAME='pvcdump-*.tar.gz.gpg' ;;
  *)         S3_NAME='pg_dump-*.pgdump.gz.gpg' ;;
esac
log "upload finished; pruning S3 objects older than ${S3_OLDER_THAN} name=${S3_NAME} (days=${S3_RETENTION_DAYS:-n/a} hours=${S3_RETENTION_HOURS:-n/a})"
# Pruning is best effort: the new dump is already uploaded, so a failed delete must not fail the Job (and re-run the whole dump). rclone retries each delete; anything left is picked up by the next run.
rclone delete --include "${S3_NAME}" --min-age "${S3_OLDER_THAN}" -v "${S3_PREFIX}/" 2>&1 \
  | while IFS= read -r line; do log "rclone: ${line}"; done || true
touch "${DATA_DIR}/.step-job-done"
log "wrote marker .step-job-done; releasing sibling containers; stage done"
