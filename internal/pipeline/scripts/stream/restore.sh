STAGE=restore
# One container: pick the object → download | decrypt | decompress → load. Postgres custom/directory dumps load with pg_restore --jobs.
# Also reads Stage-mode objects (*.gz.gpg plain SQL / RDB) so a Restore can switch modes without new backups.
stream_init
s3_init
ENGINE="${ENGINE:-postgres}"
JOBS="${JOBS:-1}"
START=$(date +%s)

case "${ENGINE}" in
  postgres) INCLUDE="pg_dump-*.gpg" ;;
  mariadb) INCLUDE="mysqldump-*.gpg" ;;
  redis) INCLUDE="redisdump-*.gpg" ;;
  *) die "restore is not supported for engine ${ENGINE}" ;;
esac

# 1. Which object.
OBJ=""
if [ -n "${BACKUP_FILE:-}" ]; then
  if [ -n "$(s3_size "${BACKUP_FILE}")" ]; then
    OBJ="${BACKUP_FILE}"
  elif truthy "${USE_LATEST_BACKUP_AS_FALLBACK:-false}"; then
    log "BACKUP_FILE ${BACKUP_FILE} not found; falling back to the newest object"
  else
    die "BACKUP_FILE ${BACKUP_FILE} not found under ${S3_PREFIX}/ and USE_LATEST_BACKUP_AS_FALLBACK is off"
  fi
elif ! truthy "${USE_LATEST_BACKUP_AS_FALLBACK:-false}"; then
  die "BACKUP_FILE is empty; set it or set USE_LATEST_BACKUP_AS_FALLBACK"
fi
if [ -z "${OBJ}" ]; then
  OBJ="$(s3_latest "${INCLUDE}")"
  [ -n "${OBJ}" ] || die "no ${INCLUDE} under ${S3_PREFIX}/"
fi
SRC="${S3_PREFIX}/${OBJ}"
log "restoring ${SRC} ($(human "$(s3_size "${OBJ}")"))"

fetch() {
  rclone cat "${SRC}" | gpg_decrypt
}

drop_allowed() { truthy "${DROP_DATABASE_IF_EXISTS:-false}"; }

restore_postgres() {
  : "${PGHOST:?}" "${PGDATABASE:?}"
  export PGOPTIONS="${PGOPTIONS:+${PGOPTIONS} }-c statement_timeout=0 -c lock_timeout=0"
  local db="${PGDATABASE}" owner="${PG_OWNER_ROLE:-${PGDATABASE}}"
  q() { psql -X -v ON_ERROR_STOP=1 -At "$@"; }
  # Identifiers go into SQL quoted; reject anything that would need escaping.
  for id in "$db" "$owner"; do
    [[ "$id" =~ ^[A-Za-z0-9_-]+$ ]] || die "unsupported identifier ${id}"
  done

  case "${OBJ}" in
    *.dump.gpg|*.dir.tar.gpg) ;;
    *)
      # Stage-mode plain SQL: no pg_restore features. Stream it into psql after the same DB reset.
      reset_database "$db" "$owner"
      log "plain SQL dump; loading with psql (no --jobs)"
      fetch | decompress_stream "${OBJ}" \
        | sed -E -e '/^SET[[:space:]]+transaction_timeout[[:space:]]*=/d' -e '/^\\(un)?restrict([[:space:]]|$)/d' \
        | psql -X -v ON_ERROR_STOP=1 -q -d "$db" > /dev/null
      return
      ;;
  esac

  # Custom (.dump) or directory (.dir.tar) format. pg_restore --jobs needs a seekable file, so the decrypted dump lands on scratch once.
  local dump
  if [[ "${OBJ}" == *.dir.tar.gpg ]]; then
    dump="${RUN_DIR}/dump"
    mkdir -p "${dump}"
    log "download | decrypt | untar → ${dump}"
    fetch | tar --extract --file=- --directory="${dump}"
  else
    dump="${RUN_DIR}/dump.pgdump"
    log "download | decrypt → ${dump}"
    fetch > "${dump}"
  fi
  log "dump on disk: $(du -sh "${dump}" | cut -f1) after $(elapsed "${START}")"

  # TimescaleDB: install the source's extension version, load in restore mode, then update.
  local meta ts_ver=""
  meta="$(rclone cat "${S3_PREFIX}/${OBJ%.gpg}.meta" 2>/dev/null || true)"
  ts_ver="$(printf '%s\n' "$meta" | sed -n 's/^SRC_TS_VERSION=//p')"
  pg_restore --list "${dump}" > "${RUN_DIR}/toc.all"
  if [ -z "${ts_ver}" ] && grep -qE ' EXTENSION - "?timescaledb"?( |$)' "${RUN_DIR}/toc.all"; then
    ts_ver="$(q -d postgres -c "SELECT default_version FROM pg_available_extensions WHERE name = 'timescaledb'")"
    log "WARN: no .meta with the source TimescaleDB version; using the target default ${ts_ver}"
  fi

  # pgAudit objects (extension, event triggers, functions) are dropped from the TOC when STRIP_PGAUDIT_EXTENSION is on.
  # TOC names are quoted with --quote-all-identifiers (EXTENSION - "pgaudit"), so match an optional quote.
  local q='"?' skip=''
  if [ -n "${ts_ver}" ]; then
    skip=" (EXTENSION - ${q}timescaledb${q}|COMMENT - EXTENSION ${q}timescaledb${q})( |\$)"
  fi
  if truthy "${STRIP_PGAUDIT_EXTENSION:-true}"; then
    skip="${skip:+${skip}|}(EXTENSION - ${q}pgaudit${q}|COMMENT - EXTENSION ${q}pgaudit${q}|EVENT TRIGGER - ${q}pgaudit_|FUNCTION [^ ]* ?${q}pgaudit_|ACL [^ ]* ?FUNCTION [^ ]* ?${q}pgaudit_)"
  fi
  if [ -n "${skip}" ]; then
    grep -v -E "${skip}" "${RUN_DIR}/toc.all" > "${RUN_DIR}/toc.list" || true
  else
    cp "${RUN_DIR}/toc.all" "${RUN_DIR}/toc.list"
  fi
  log "TOC: $(wc -l < "${RUN_DIR}/toc.all") entries, $(( $(wc -l < "${RUN_DIR}/toc.all") - $(wc -l < "${RUN_DIR}/toc.list") )) skipped (extension/pgAudit)"

  # Roles the dump names (owners and grants). Missing ones are created NOLOGIN.
  # Same anchors as the Stage restore: grantees end the statement (TO "x"; / FROM "x";), so schema names such as "public" in ON SCHEMA "public" TO ... are not read as roles.
  pg_restore --schema-only --use-list="${RUN_DIR}/toc.list" --file=- "${dump}" \
    | grep -oE '(OWNER TO "[^"]+"|FOR ROLE "[^"]+"| TO "[^"]+";| FROM "[^"]+";)' \
    | sed -E 's/^(OWNER TO|FOR ROLE| TO| FROM) "//; s/";?$//' | sort -u > "${RUN_DIR}/roles" || true
  ensure_role "$owner" LOGIN
  while IFS= read -r role; do
    ensure_role "$role" NOLOGIN
  done < "${RUN_DIR}/roles"

  reset_database "$db" "$owner"

  local t
  pgr() { pg_restore --dbname="$db" --exit-on-error --no-password "$@" "${dump}"; }
  if [ -n "${ts_ver}" ]; then
    [ "$(q -d postgres -c "SELECT count(*) FROM pg_available_extension_versions WHERE name = 'timescaledb' AND version = '${ts_ver}'")" = 1 ] \
      || die "TimescaleDB ${ts_ver} (the dump's version) is not installed on ${PGHOST}; its catalog cannot be loaded"
    log "TimescaleDB ${ts_ver}: CREATE EXTENSION, timescaledb_pre_restore()"
    q -d "$db" -c "CREATE EXTENSION timescaledb VERSION '${ts_ver}' SCHEMA public" -c "SELECT public.timescaledb_pre_restore()" > /dev/null
    # Catalog tables get foreign keys from CREATE EXTENSION: their data loads in dump order on one job.
    grep -E ' TABLE DATA _timescaledb_(catalog|config) ' "${RUN_DIR}/toc.list" > "${RUN_DIR}/toc-catalog.list" || true
    grep -v -E ' TABLE DATA _timescaledb_(catalog|config) ' "${RUN_DIR}/toc.list" > "${RUN_DIR}/toc-rest.list"
    t=$(date +%s); pgr --section=pre-data --use-list="${RUN_DIR}/toc.list"; log "schema in $(elapsed "$t")"
    t=$(date +%s); pgr --section=data --use-list="${RUN_DIR}/toc-catalog.list"; log "TimescaleDB catalog data in $(elapsed "$t")"
    t=$(date +%s); pgr --section=data --use-list="${RUN_DIR}/toc-rest.list" --jobs="${JOBS}"; log "data (--jobs=${JOBS}) in $(elapsed "$t")"
    t=$(date +%s); pgr --section=post-data --use-list="${RUN_DIR}/toc.list" --jobs="${JOBS}"; log "indexes and constraints in $(elapsed "$t")"
    q -d "$db" -c "SELECT public.timescaledb_post_restore()" > /dev/null
    # New session: ALTER EXTENSION must run before the old library loads.
    q -d "$db" -c "ALTER EXTENSION timescaledb UPDATE" > /dev/null
    log "TimescaleDB ${ts_ver} → $(q -d "$db" -c "SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'")"
  else
    t=$(date +%s)
    pgr --use-list="${RUN_DIR}/toc.list" --jobs="${JOBS}"
    log "pg_restore --jobs=${JOBS} in $(elapsed "$t")"
  fi
  rm -rf "${dump}"
  if truthy "${ANALYZE_AFTER_RESTORE:-true}"; then
    t=$(date +%s)
    vacuumdb --analyze-only --jobs="${JOBS}" --dbname="$db" --quiet
    log "ANALYZE in $(elapsed "$t")"
  fi
  log "database size $(q -d postgres -c "SELECT pg_size_pretty(pg_database_size('${db}'))")"
}

ensure_role() {
  local role="$1" login="${2:-NOLOGIN}"
  case "${role,,}" in ''|public|current_user|session_user|current_role|postgres|pg_*) return 0 ;; esac
  [[ "$role" =~ ^[A-Za-z0-9_.-]+$ ]] || { log "WARN: skipping role with unsupported name ${role}"; return 0; }
  psql -X -v ON_ERROR_STOP=1 -q -d postgres -c "DO \$\$ BEGIN CREATE ROLE \"${role}\" ${login}; EXCEPTION WHEN duplicate_object THEN NULL; END \$\$;"
}

# Drop (when allowed and non-empty) and create the target database from template0.
reset_database() {
  local db="$1" owner="$2" exists objs
  exists="$(psql -X -At -d postgres -c "SELECT 1 FROM pg_database WHERE datname = '${db}'")"
  if [ "${exists}" = 1 ]; then
    objs="$(psql -X -At -d "$db" -c "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema','pg_toast') AND n.nspname NOT LIKE 'pg_temp_%' AND c.relkind IN ('r','p','v','m','S','f')")"
    if [ "${objs}" -gt 0 ] && ! drop_allowed; then
      die "database ${db} exists with ${objs} objects; set dropDatabaseIfExists to replace it"
    fi
    log "dropping ${db} (${objs} objects)"
    psql -X -v ON_ERROR_STOP=1 -q -d postgres -c "DROP DATABASE \"${db}\" WITH (FORCE)"
  fi
  log "creating ${db} owner ${owner}"
  psql -X -v ON_ERROR_STOP=1 -q -d postgres -c "CREATE DATABASE \"${db}\" OWNER \"${owner}\" TEMPLATE template0"
}

restore_mariadb() {
  : "${MYSQL_HOST:?}" "${MYSQL_DATABASE:?}"
  export MYSQL_PWD="${MYSQL_PWD:-${MYSQL_PASSWORD:?MYSQL_PASSWORD required}}"
  local db="${MYSQL_DATABASE}"
  [[ "$db" =~ ^[A-Za-z0-9_-]+$ ]] || die "unsupported database name ${db}"
  my() { mariadb --host="${MYSQL_HOST}" --port="${MYSQL_PORT:-3306}" --user="${MYSQL_USER}" "$@"; }
  local objs
  objs="$(my -N -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = '${db}'")"
  if [ "${objs}" -gt 0 ]; then
    drop_allowed || die "database ${db} has ${objs} tables; set dropDatabaseIfExists to replace it"
    log "dropping ${db} (${objs} tables)"
    my -e "DROP DATABASE \`${db}\`"
  fi
  my -e "CREATE DATABASE IF NOT EXISTS \`${db}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
  local t; t=$(date +%s)
  log "download | decrypt | decompress | mariadb"
  {
    echo "SET SESSION unique_checks=0; SET SESSION foreign_key_checks=0; SET SESSION autocommit=0;"
    # Stage-mode dumps use --databases (CREATE DATABASE + USE <source>): drop those so the data lands in the target name.
    fetch | decompress_stream "${OBJ}" | sed -E \
      -e '/^CREATE DATABASE /d' \
      -e '/^USE `[^`]*`;$/d' \
      -e '/^SET[[:space:]]+@@GLOBAL\.GTID_PURGED/d' \
      -e '/^SET[[:space:]]+@@SESSION\.SQL_LOG_BIN/d' \
      -e 's/DEFINER=`[^`]*`@`[^`]*`/DEFINER=CURRENT_USER/g'
    echo "COMMIT; SET SESSION unique_checks=1; SET SESSION foreign_key_checks=1;"
  } | my --database="${db}"
  log "mariadb load in $(elapsed "$t")"
}

# Redis: load the RDB into a temporary redis-server in this pod (:6380), have the target REPLICAOF it, then promote the target. The target must reach the Job pod on :6380.
restore_redis() {
  : "${REDIS_HOST:?}"
  local user_args=()
  if [ -n "${REDIS_USERNAME:-}" ] && [ "${REDIS_USERNAME}" != "default" ]; then
    user_args=(--user "${REDIS_USERNAME}")
  fi
  if [ -n "${REDIS_PASSWORD:-}" ]; then export REDISCLI_AUTH="${REDIS_PASSWORD}"; else unset REDISCLI_AUTH; fi
  target() { redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT:-6379}" "${user_args[@]}" "$@"; }
  local_redis() { env -u REDISCLI_AUTH redis-cli -h 127.0.0.1 -p 6380 "$@"; }

  local keys
  keys="$(target DBSIZE | tr -d '\r')"
  if [ "${keys}" != 0 ] && ! drop_allowed; then
    die "target redis has ${keys} keys; set dropDatabaseIfExists to replace it"
  fi

  local dir="${RUN_DIR}/redis"
  mkdir -p "${dir}"
  log "download | decrypt | decompress → ${dir}/dump.rdb"
  fetch | decompress_stream "${OBJ}" > "${dir}/dump.rdb"
  log "RDB $(human "$(stat -c %s "${dir}/dump.rdb")"); starting redis-server on :6380"
  redis-server --daemonize yes --port 6380 --bind 0.0.0.0 --protected-mode no --dir "${dir}" --dbfilename dump.rdb \
    --appendonly no --save "" --logfile "${dir}/server.log"
  local i=0
  until [ "$(local_redis INFO persistence 2>/dev/null | tr -d '\r' | sed -n 's/^loading://p')" = 0 ]; do
    i=$((i + 1))
    [ "$i" -le 1800 ] || { tail -n 20 "${dir}/server.log" >&2 || true; die "redis-server did not finish loading the RDB"; }
    sleep 1
  done
  local src_keys pod_ip
  src_keys="$(local_redis DBSIZE | tr -d '\r')"
  pod_ip="$(hostname -i | awk '{print $1}')"
  [ -n "${pod_ip}" ] || die "cannot find the pod IP for REPLICAOF"
  log "loaded ${src_keys} keys; target ${REDIS_HOST} REPLICAOF ${pod_ip} 6380"
  target CONFIG SET masterauth "" > /dev/null
  target REPLICAOF "${pod_ip}" 6380 > /dev/null
  i=0
  while :; do
    local info link sync
    info="$(target INFO replication | tr -d '\r')"
    link="$(printf '%s\n' "$info" | sed -n 's/^master_link_status://p')"
    sync="$(printf '%s\n' "$info" | sed -n 's/^master_sync_in_progress://p')"
    [ "$link" = up ] && [ "${sync:-1}" = 0 ] && break
    i=$((i + 1))
    if [ "$i" -gt 1800 ]; then
      target REPLICAOF NO ONE > /dev/null 2>&1 || true
      die "replication did not finish (link=${link:-?} sync=${sync:-?})"
    fi
    sleep 2
  done
  target REPLICAOF NO ONE > /dev/null
  log "target promoted; DBSIZE $(target DBSIZE | tr -d '\r') (RDB ${src_keys}; keys with TTL may have expired)"
  local_redis SHUTDOWN NOSAVE > /dev/null 2>&1 || true
}

case "${ENGINE}" in
  postgres) restore_postgres ;;
  mariadb) restore_mariadb ;;
  redis) restore_redis ;;
esac
log "restore done in $(elapsed "${START}")"
