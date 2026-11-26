# Stream pipeline helpers (one container: spec.pipeline.mode Stream). Bash: the pipes need pipefail.
set -Eeuo pipefail
umask 0077

log() {
  local msg
  msg="[${STAGE:-stream} $(date '+%Y-%m-%dT%H:%M:%S%z')] $*"
  echo "$msg" >&2
  if [ -n "${LOG_FILE:-}" ]; then
    echo "$msg" >> "${LOG_FILE}" 2>/dev/null || true
  fi
}

die() {
  log "ERROR: $*"
  exit 1
}

truthy() {
  case "${1:-}" in
    1|yes|YES|true|TRUE) return 0 ;;
    *) return 1 ;;
  esac
}

elapsed() {
  echo "$(( $(date +%s) - $1 ))s"
}

human() {
  numfmt --to=iec-i --suffix=B "${1:-0}" 2>/dev/null || echo "${1:-0} bytes"
}

# Log file on the volume (optional), pruned with the local retention.
stream_init() {
  : "${WORK_ROOT:?WORK_ROOT required}"
  mkdir -p "${WORK_ROOT}"
  RUN_DIR="${WORK_ROOT}/run-${HOSTNAME:-local}"
  rm -rf "${RUN_DIR}"
  mkdir -p "${RUN_DIR}"
  if truthy "${LOG_FILE_ENABLED:-false}"; then
    mkdir -p "${WORK_ROOT}/logs"
    LOG_FILE="${WORK_ROOT}/logs/${HOSTNAME:-stream}.log"
    : > "${LOG_FILE}"
  fi
  encryption_init
  trap 'rc=$?; rm -rf "${RUN_DIR}"; [ "$rc" -eq 0 ] || log "ERROR: failed with exit ${rc}"; exit "$rc"' EXIT
}

# rclone remote "s3:" from env only (no config file). Credentials come from S3_ACCESS_KEY / S3_SECRET_KEY.
s3_init() {
  : "${S3_ENDPOINT:?S3_ENDPOINT required}" "${S3_BUCKET:?S3_BUCKET required}" "${S3_PATH:?S3_PATH required}"
  : "${S3_ACCESS_KEY:?S3_ACCESS_KEY required}" "${S3_SECRET_KEY:?S3_SECRET_KEY required}"
  export RCLONE_CONFIG_S3_TYPE=s3
  export RCLONE_CONFIG_S3_PROVIDER="${S3_PROVIDER:-Other}"
  export RCLONE_CONFIG_S3_ENDPOINT="${S3_ENDPOINT}"
  export RCLONE_CONFIG_S3_ACCESS_KEY_ID="${S3_ACCESS_KEY}"
  export RCLONE_CONFIG_S3_SECRET_ACCESS_KEY="${S3_SECRET_KEY}"
  export RCLONE_CONFIG_S3_REGION="${S3_REGION:-us-east-1}"
  export RCLONE_CONFIG_S3_FORCE_PATH_STYLE="${S3_FORCE_PATH_STYLE:-true}"
  export RCLONE_CONFIG_S3_NO_CHECK_BUCKET=true
  export RCLONE_CONFIG="${RUN_DIR}/rclone.conf"
  export RCLONE_CACHE_DIR="${RUN_DIR}/rclone-cache"
  : > "${RCLONE_CONFIG}"
  # Multipart chunks are buffered in memory per upload stream: chunk size x concurrency.
  export RCLONE_S3_CHUNK_SIZE="${S3_CHUNK_SIZE:-64Mi}"
  export RCLONE_S3_UPLOAD_CONCURRENCY="${S3_UPLOAD_CONCURRENCY:-4}"
  export RCLONE_RETRIES="${S3_RETRIES:-5}"
  export RCLONE_LOW_LEVEL_RETRIES="${S3_LOW_LEVEL_RETRIES:-10}"
  export RCLONE_STATS_LOG_LEVEL=NOTICE
  # Ages and "latest" come from the S3 LastModified (upload time): no HEAD request per object.
  export RCLONE_USE_SERVER_MODTIME=true
  S3_PREFIX="s3:${S3_BUCKET}/${S3_PATH%/}"
}

# spec.encryption.method: gpg (default, symmetric passphrase) or age (X25519).
# A Backup with age holds only the public key(s) (AGE_RECIPIENTS_FILE); a Restore holds the private key (AGE_IDENTITY_FILE).
# ENC_EXT is the object suffix: gpg or age.
encryption_init() {
  ENCRYPTION_METHOD="${ENCRYPTION_METHOD:-gpg}"
  case "${ENCRYPTION_METHOD}" in
    gpg)
      ENC_EXT=gpg
      export GNUPGHOME="${RUN_DIR}/gnupg"
      mkdir -p "${GNUPGHOME}"
      chmod 700 "${GNUPGHOME}"
      GPG_PASSFILE="${GPG_PASSFILE:-/run/secrets/gpg/gpg_passphrase}"
      [ -s "${GPG_PASSFILE}" ] || die "gpg passphrase file ${GPG_PASSFILE} is missing or empty"
      ;;
    age)
      ENC_EXT=age
      case "${STAGE:-}" in
        backup)
          AGE_RECIPIENTS_FILE="${AGE_RECIPIENTS_FILE:-/run/secrets/age/age_recipient}"
          [ -s "${AGE_RECIPIENTS_FILE}" ] || die "age recipients file ${AGE_RECIPIENTS_FILE} is missing or empty"
          ;;
        *)
          AGE_IDENTITY_FILE="${AGE_IDENTITY_FILE:-/run/secrets/age/age_identity}"
          [ -s "${AGE_IDENTITY_FILE}" ] || die "age identity file ${AGE_IDENTITY_FILE} is missing or empty"
          ;;
      esac
      ;;
    *) die "unknown ENCRYPTION_METHOD ${ENCRYPTION_METHOD} (gpg or age)" ;;
  esac
}

encrypt_stream() {
  if [ "${ENCRYPTION_METHOD}" = age ]; then
    age --encrypt --recipients-file "${AGE_RECIPIENTS_FILE}"
  else
    gpg --batch --yes --quiet --pinentry-mode loopback --passphrase-file "${GPG_PASSFILE}" \
      --symmetric --cipher-algo AES256 --compress-algo none --output -
  fi
}

decrypt_stream() {
  if [ "${ENCRYPTION_METHOD}" = age ]; then
    age --decrypt --identity "${AGE_IDENTITY_FILE}"
  else
    gpg --batch --yes --quiet --pinentry-mode loopback --passphrase-file "${GPG_PASSFILE}" --decrypt
  fi
}

# Compression level for zstd (1-19). pg_dump compresses itself.
zstd_level() {
  local lvl="${COMPRESSION_LEVEL:-3}"
  case "$lvl" in ''|*[!0-9]*) lvl=3 ;; esac
  echo "$lvl"
}

compress_stream() {
  zstd -q -T0 "-$(zstd_level)" -c
}

# Decompress by object suffix: .zst (Stream) or .gz (Stage mode, readable by Stream restores); encrypted with .gpg or .age.
decompress_stream() {
  case "$1" in
    *.zst.gpg|*.zst.age) zstd -q -d -c ;;
    *.gz.gpg|*.gz.age) pigz -d -c 2>/dev/null || gzip -d -c ;;
    *) cat ;;
  esac
}

# Size in bytes of one object (empty when missing).
s3_size() {
  rclone lsf --files-only --format s --include "$1" "${S3_PREFIX}/" 2>/dev/null | head -n 1
}

# Delete objects older than the retention. Best effort: a failed delete is logged and left for the next run.
s3_prune() {
  local days="${S3_RETENTION_DAYS:-14}" include="$1"
  log "pruning ${S3_PREFIX}/ objects matching ${include} older than ${days}d"
  local listing="${RUN_DIR}/expired"
  if ! rclone lsf --files-only --include "${include}" --min-age "${days}d" "${S3_PREFIX}/" > "${listing}" 2> "${RUN_DIR}/lsf.err"; then
    log "WARN: listing for prune failed; leaving old objects for the next run: $(tail -n 3 "${RUN_DIR}/lsf.err" | tr '\n' ' ')"
    return 0
  fi
  if [ ! -s "${listing}" ]; then
    log "prune: nothing older than ${days}d"
    return 0
  fi
  while IFS= read -r obj; do
    log "prune: ${obj}"
  done < "${listing}"
  if rclone delete --files-from-raw "${listing}" "${S3_PREFIX}/" 2> "${RUN_DIR}/delete.err"; then
    log "prune: removed $(wc -l < "${listing}") object(s)"
  else
    log "WARN: some deletes failed; they are retried on the next run: $(tail -n 3 "${RUN_DIR}/delete.err" | tr '\n' ' ')"
  fi
  return 0
}

# Newest object matching the include pattern, by modification time (not by name).
s3_latest() {
  local include="$1"
  rclone lsf --files-only --format tp --separator '|' --include "${include}" "${S3_PREFIX}/" \
    | sort | tail -n 1 | cut -d'|' -f2-
}
