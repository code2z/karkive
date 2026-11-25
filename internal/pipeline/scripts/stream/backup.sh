STAGE=backup
# One container, one pass: local cleanup → dump | compress | encrypt | upload → S3 prune.
# Postgres compresses in pg_dump (custom format, zstd) so pg_restore --jobs works on the result; the other engines go through zstd -T0.
stream_init
s3_enabled=true
truthy "${S3_ENABLED:-true}" || s3_enabled=false

ENGINE="${ENGINE:-postgres}"
TS="$(date -u '+%Y-%m-%d-%H-%M-%S')"
LVL="$(zstd_level)"
START=$(date +%s)

# 1. Local cleanup: run dirs of earlier pods, and retained dumps past the local retention (when S3 is off).
RETAINED="${WORK_ROOT}/retained"
find "${WORK_ROOT}" -mindepth 1 -maxdepth 1 -type d -name 'run-*' ! -path "${RUN_DIR}" -exec rm -rf {} + 2>/dev/null || true
if [ -d "${RETAINED}" ]; then
  find "${RETAINED}" -type f \( -name '*.gpg' -o -name '*.meta' \) -mtime "+${LOCAL_RETENTION_DAYS:-7}" -print -delete 2>/dev/null | while IFS= read -r f; do log "removed expired local ${f}"; done || true
fi
if truthy "${LOG_FILE_ENABLED:-false}"; then
  find "${WORK_ROOT}/logs" -type f -name '*.log' -mtime "+${LOCAL_RETENTION_DAYS:-7}" -delete 2>/dev/null || true
fi

# 2. The dump command for this engine writes the compressed stream to stdout.
case "${ENGINE}" in
  postgres)
    : "${PGHOST:?}" "${PGDATABASE:?}"
    export PGOPTIONS="${PGOPTIONS:+${PGOPTIONS} }-c statement_timeout=0 -c lock_timeout=0 -c idle_in_transaction_session_timeout=0"
    JOBS="${JOBS:-1}"
    INCLUDE="pg_dump-*.gpg"
    src_ver="$(psql -X -At -c 'SHOW server_version' 2>/dev/null | cut -d' ' -f1 || true)"
    ts_ver="$(psql -X -At -c "SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'" 2>/dev/null || true)"
    log "source ${PGHOST}:${PGPORT:-5432}/${PGDATABASE}: PostgreSQL ${src_ver:-?}, $(psql -X -At -c 'SELECT pg_size_pretty(pg_database_size(current_database()))' 2>/dev/null || echo '? size')${ts_ver:+, TimescaleDB ${ts_ver}}"
    # Metadata travels next to the dump so a restore can install the source's TimescaleDB version first.
    META="SRC_PG_VERSION=${src_ver}
SRC_TS_VERSION=${ts_ver}
DUMP_STARTED_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    # TimescaleDB job run statistics are not needed on a restore target.
    pg_extra=()
    if [ -n "${ts_ver}" ]; then
      pg_extra+=(--exclude-table-data=_timescaledb_internal.bgw_job_stat --exclude-table-data=_timescaledb_internal.bgw_job_stat_history)
    fi
    if [ "${JOBS}" -gt 1 ]; then
      # Parallel dump needs directory format: dump to scratch (compressed size), then tar | gpg | upload.
      NAME="pg_dump-${PGDATABASE}-${TS}.dir.tar.gpg"
      dump() {
        local d="${RUN_DIR}/dump"
        log "pg_dump --format=directory --jobs=${JOBS} --compress=zstd:${LVL} → ${d}"
        # shellcheck disable=SC2086
        pg_dump --format=directory --jobs="${JOBS}" --compress="zstd:${LVL}" --no-password \
          --quote-all-identifiers --load-via-partition-root "${pg_extra[@]}" ${PG_DUMP_EXTRA_ARGS:-} --file="${d}" >&2
        log "dump on disk: $(du -sh "${d}" | cut -f1) in $(elapsed "$t"); streaming tar"
        tar --create --file=- --directory="${d}" .
      }
    else
      NAME="pg_dump-${PGDATABASE}-${TS}.dump.gpg"
      dump() {
        # shellcheck disable=SC2086
        pg_dump --format=custom --compress="zstd:${LVL}" --no-password \
          --quote-all-identifiers --load-via-partition-root "${pg_extra[@]}" ${PG_DUMP_EXTRA_ARGS:-}
      }
    fi
    ;;
  mariadb)
    : "${MYSQL_HOST:?}" "${MYSQL_DATABASE:?}"
    export MYSQL_PWD="${MYSQL_PWD:-${MYSQL_PASSWORD:?MYSQL_PASSWORD required}}"
    NAME="mysqldump-${MYSQL_DATABASE}-${TS}.sql.zst.gpg"
    INCLUDE="mysqldump-*.gpg"
    dump() {
      # No --databases: the dump has no CREATE DATABASE / USE, so a Restore can load it into any database name.
      mariadb-dump --host="${MYSQL_HOST}" --port="${MYSQL_PORT:-3306}" --user="${MYSQL_USER}" \
        --single-transaction --quick --routines --triggers --events --hex-blob \
        --default-character-set=utf8mb4 --max-statement-time=0 "${MYSQL_DATABASE}"
    }
    dump_compressed() { dump | compress_stream; }
    ;;
  redis)
    : "${REDIS_HOST:?}"
    # An empty REDISCLI_AUTH still sends AUTH "", which a server without a password rejects.
    if [ -n "${REDISCLI_AUTH:-${REDIS_PASSWORD:-}}" ]; then
      export REDISCLI_AUTH="${REDISCLI_AUTH:-${REDIS_PASSWORD}}"
    else
      unset REDISCLI_AUTH
    fi
    user_args=()
    if [ -n "${REDIS_USERNAME:-}" ] && [ "${REDIS_USERNAME}" != "default" ]; then
      user_args=(--user "${REDIS_USERNAME}")
    fi
    NAME="redisdump-${REDIS_NAME:-redis}-${TS}.rdb.zst.gpg"
    INCLUDE="redisdump-*.gpg"
    dump() {
      local ec=0
      redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT:-6379}" "${user_args[@]}" --rdb - 2> "${RUN_DIR}/redis-cli.err" || ec=$?
      if [ "$ec" -ne 0 ]; then
        while IFS= read -r line; do log "redis-cli: ${line}"; done < "${RUN_DIR}/redis-cli.err"
        return "$ec"
      fi
    }
    dump_compressed() { dump | compress_stream; }
    ;;
  pvc)
    : "${PVC_SOURCE_DIR:?}" "${PVC_CLAIM_NAME:?}"
    SRC="${PVC_SOURCE_DIR}${PVC_PATH:+/${PVC_PATH}}"
    [ -d "${SRC}" ] || die "source path ${SRC} is not a directory"
    printf '%s\n' "${PVC_EXCLUDES:-}" | sed '/^$/d' > "${RUN_DIR}/excludes"
    NAME="pvcdump-${PVC_CLAIM_NAME}-${TS}.tar.zst.gpg"
    INCLUDE="pvcdump-*.gpg"
    dump() {
      local ec=0
      # GNU tar exit 1 = files changed while read: the archive is still valid.
      tar --create --file=- --directory="${SRC}" --numeric-owner --warning=no-file-changed \
        --exclude-from="${RUN_DIR}/excludes" . || ec=$?
      [ "$ec" -le 1 ] || return "$ec"
    }
    dump_compressed() { dump | compress_stream; }
    ;;
  *) die "unknown ENGINE ${ENGINE}" ;;
esac
# Postgres compresses inside pg_dump; the other engines pipe through zstd.
run_dump() {
  if declare -F dump_compressed > /dev/null; then dump_compressed; else dump; fi
}

# 3. dump | encrypt | upload, as one pipe. rclone rcat streams a multipart upload; nothing touches the disk.
if [ "${s3_enabled}" = true ]; then
  s3_init
  DEST="${S3_PREFIX}/${NAME}"
  log "streaming ${ENGINE} dump → ${DEST} (zstd level ${LVL})"
  t=$(date +%s)
  run_dump | gpg_encrypt | rclone rcat --s3-chunk-size "${RCLONE_S3_CHUNK_SIZE}" "${DEST}"
  size="$(s3_size "${NAME}")"
  if [ -z "${size}" ] || [ "${size}" -eq 0 ]; then
    die "upload of ${NAME} not found after rcat"
  fi
  log "uploaded ${NAME} ($(human "${size}")) in $(elapsed "$t")"
  if [ -n "${META:-}" ]; then
    printf '%s\n' "${META}" | rclone rcat "${S3_PREFIX}/${NAME%.gpg}.meta" || log "WARN: could not upload ${NAME%.gpg}.meta"
  fi
  # 4. S3 retention (best effort, never fails a backup that is already uploaded). The pattern also matches the .meta sidecars.
  s3_prune "${INCLUDE%.gpg}"
else
  mkdir -p "${RETAINED}"
  OUT="${RETAINED}/${NAME}"
  log "streaming ${ENGINE} dump → ${OUT} (S3 disabled; zstd level ${LVL})"
  t=$(date +%s)
  run_dump | gpg_encrypt > "${OUT}.part"
  mv "${OUT}.part" "${OUT}"
  [ -z "${META:-}" ] || printf '%s\n' "${META}" > "${RETAINED}/${NAME%.gpg}.meta"
  log "wrote ${OUT} ($(human "$(stat -c %s "${OUT}")")) in $(elapsed "$t")"
fi
log "backup done in $(elapsed "${START}")"
