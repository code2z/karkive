set -eu
umask 0002
STAGE=encrypt
# Shared PVC is reused across Jobs; scope scratch to this pod
# so peers never see stale .step-* markers from a prior run.
DATA_ROOT="${DATA_DIR:-${PGDUMP_DIR:?DATA_DIR or PGDUMP_DIR required}}"
DATA_DIR="${DATA_ROOT}/${HOSTNAME}"
pipeline_init
wait_for "${DATA_DIR}/.step-compress-done" "compress"
already_done_hold "${DATA_DIR}/.step-encrypt-done" "encrypt"
log "stage start: encrypt method=${ENCRYPTION_METHOD}"
DUMP_PREFIX="${DUMP_PREFIX:-pg_dump}"
case "${DUMP_PREFIX}" in
  mysqldump) GZ_GLOB='mysqldump-*.sql.gz' ;;
  redisdump) GZ_GLOB='redisdump-*.rdb.gz' ;;
  pvcdump)   GZ_GLOB='pvcdump-*.tar.gz' ;;
  *)         GZ_GLOB='pg_dump-*.pgdump.gz' ;;
esac
ls "${DATA_DIR}"/${GZ_GLOB} >/dev/null 2>&1 || {
  log "ERROR: no ${GZ_GLOB} after compress marker" >&2
  mark_failed
  exit 1
}
if [ "${ENCRYPTION_METHOD}" = age ]; then
  # Public key(s) only: this Backup cannot decrypt what it writes.
  AGE_RECIPIENTS_FILE="${AGE_RECIPIENTS_FILE:-/run/secrets/age/age_recipient}"
  if [ ! -s "${AGE_RECIPIENTS_FILE}" ]; then
    log "ERROR: age recipients file ${AGE_RECIPIENTS_FILE} is missing or empty" >&2
    mark_failed
    exit 1
  fi
else
  export GNUPGHOME="/tmp/gpg-home"
  mkdir -p "$GNUPGHOME" && chmod 700 "$GNUPGHOME"
fi
found=0
for f in "${DATA_DIR}"/${GZ_GLOB}; do
  [ -f "$f" ] || continue
  found=1
  out="${f}.${ENC_EXT}"
  log "${ENCRYPTION_METHOD} encrypt ${f} ($(wc -c < "$f") bytes)"
  if [ "${ENCRYPTION_METHOD}" = age ]; then
    age --encrypt --recipients-file "${AGE_RECIPIENTS_FILE}" --output "${out}" "$f"
  else
    gpg --batch --yes --symmetric --cipher-algo AES256 \
        --passphrase-file /run/secrets/gpg/gpg_passphrase \
        --output "${out}" "$f"
  fi
  rm "$f"
  log "${ENCRYPTION_METHOD} done -> ${out} ($(wc -c < "${out}") bytes); removed plaintext gz"
  mkdir -p "${DATA_ROOT}/retained"
  cp -f "${out}" "${DATA_ROOT}/retained/"
  log "kept local copy ${DATA_ROOT}/retained/$(basename "${out}") (LOCAL_RETENTION_DAYS=${LOCAL_RETENTION_DAYS:-7})"
done
if [ "$found" -eq 0 ]; then
  log "ERROR: no ${GZ_GLOB} files to encrypt" >&2
  mark_failed
  exit 1
fi
touch "${DATA_DIR}/.step-encrypt-done"
log "wrote marker .step-encrypt-done; stage work done"
s3_enabled() {
  case "${S3_ENABLED:-true}" in
    0|no|NO|false|FALSE) return 1 ;;
    *) return 0 ;;
  esac
}
if s3_enabled; then
  hold_until_job_done
else
  log "S3 disabled; retained/ is the copy of record"
  touch "${DATA_DIR}/.step-job-done"
  log "wrote marker .step-job-done; releasing sibling containers; stage done"
fi
