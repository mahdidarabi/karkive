set -eu
umask 0002
STAGE=pgrestore
# Shared PVC is reused across Jobs; scope scratch to this pod
# so peers never see stale .step-* markers from a prior run.
WORKDIR_ROOT="${WORKDIR}"
WORKDIR="${WORKDIR_ROOT}/${HOSTNAME}"
pipeline_init
wait_for "${WORKDIR}/.step-extract-done" "extract"
already_done_exit "${WORKDIR}/.step-job-done" "pgrestore"
log "stage start: pgrestore database=${PGDATABASE} host=${PGHOST}:${PGPORT}"
DUMP="${WORKDIR}/dump"
test -f "$DUMP"
log "dump file size=$(wc -c < "$DUMP") bytes"

drop_allowed() {
  case "${DROP_DATABASE_IF_EXISTS:-}" in
    yes|YES|true|TRUE|1) return 0 ;;
    *) return 1 ;;
  esac
}

strip_pgaudit_enabled() {
  case "${STRIP_PGAUDIT_EXTENSION:-}" in
    yes|YES|true|TRUE|1) return 0 ;;
    *) return 1 ;;
  esac
}

# pg_dump --quote-all-identifiers emits these even after CREATE EXTENSION is
# stripped: event triggers EXECUTE the C functions, and GRANT/REVOKE target
# them. Restore targets often do not preload pgaudit, so the functions are absent.
strip_pgaudit_ddl() {
  sed -E \
    -e '/^DROP EXTENSION([[:space:]]+IF[[:space:]]+EXISTS)?[[:space:]]+("pgaudit"|pgaudit)/d' \
    -e '/^CREATE EXTENSION([[:space:]]+IF[[:space:]]+NOT[[:space:]]+EXISTS)?[[:space:]]+("pgaudit"|pgaudit)/d' \
    -e '/^COMMENT ON EXTENSION[[:space:]]+("pgaudit"|pgaudit)/d' \
    -e '/^DROP EVENT TRIGGER([[:space:]]+IF[[:space:]]+EXISTS)?[[:space:]]+"?pgaudit_/d' \
    -e '/^CREATE EVENT TRIGGER[[:space:]]+"?pgaudit_/,/;[[:space:]]*$/d' \
    -e '/^ALTER EVENT TRIGGER[[:space:]]+"?pgaudit_/d' \
    -e '/^COMMENT ON EVENT TRIGGER[[:space:]]+"?pgaudit_/d' \
    -e '/^DROP FUNCTION([[:space:]]+IF[[:space:]]+EXISTS)?[[:space:]].*pgaudit_(ddl_command_end|sql_drop)/d' \
    -e '/^CREATE FUNCTION[[:space:]].*pgaudit_(ddl_command_end|sql_drop)/,/;[[:space:]]*$/d' \
    -e '/^ALTER FUNCTION[[:space:]].*pgaudit_(ddl_command_end|sql_drop)/,/;[[:space:]]*$/d' \
    -e '/^COMMENT ON FUNCTION[[:space:]].*pgaudit_(ddl_command_end|sql_drop)/d' \
    -e '/^GRANT[[:space:]].*ON FUNCTION[[:space:]].*pgaudit_(ddl_command_end|sql_drop)/d' \
    -e '/^REVOKE[[:space:]].*ON FUNCTION[[:space:]].*pgaudit_(ddl_command_end|sql_drop)/d'
}

strip_timescale_ddl() {
  sed -E \
    -e '/^DROP EXTENSION([[:space:]]+IF[[:space:]]+EXISTS)?[[:space:]]+("timescaledb"|timescaledb)/d' \
    -e '/^CREATE EXTENSION([[:space:]]+IF[[:space:]]+NOT[[:space:]]+EXISTS)?[[:space:]]+("timescaledb"|timescaledb)/d' \
    -e '/^COMMENT ON EXTENSION[[:space:]]+("timescaledb"|timescaledb)/d'
}

strip_pgdump_client_meta() {
  sed -E \
    -e '/^SET[[:space:]]+transaction_timeout[[:space:]]*=/d' \
    -e '/^\\restrict([[:space:]]|$)/d' \
    -e '/^\\unrestrict([[:space:]]|$)/d'
}

filter_dump() {
  out="$1"
  strip_timescale="$2"
  # Dash /bin/sh has no pipefail; each stage reads a real file.
  meta="${out}.meta"
  strip_pgdump_client_meta < "$DUMP" > "$meta"
  if strip_pgaudit_enabled; then
    next="${out}.pgaudit"
    strip_pgaudit_ddl < "$meta" > "$next"
    rm -f "$meta"
    meta="$next"
  fi
  if [ "$strip_timescale" -eq 1 ]; then
    strip_timescale_ddl < "$meta" > "$out"
    rm -f "$meta"
  else
    mv "$meta" "$out"
  fi
}

ensure_role() {
  role="$1"
  login="${2:-NOLOGIN}"
  [ -n "$role" ] || return 0
  case "$role" in
    PUBLIC|CURRENT_USER|SESSION_USER|CURRENT_ROLE|postgres) return 0 ;;
  esac
  log "ensuring role ${role} (${login})"
  psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d postgres -c "
    DO \$\$
    BEGIN
      CREATE ROLE \"${role}\" NOSUPERUSER NOCREATEDB NOCREATEROLE ${login};
    EXCEPTION WHEN duplicate_object THEN
      NULL;
    END
    \$\$;"
}

OWNER_ROLE="${PG_OWNER_ROLE:-${PGDATABASE}}"
log "ensuring dump owner role ${OWNER_ROLE} exists"
ensure_role "${OWNER_ROLE}" LOGIN

DB_EXISTS="$(psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d postgres -Atc \
  "SELECT 1 FROM pg_database WHERE datname = '${PGDATABASE}'")"
if [ "${DB_EXISTS}" = "1" ]; then
  OBJ_COUNT="$(psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d "${PGDATABASE}" -Atc \
    "SELECT count(*) FROM pg_class c
     JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
       AND n.nspname NOT LIKE 'pg_temp_%'
       AND n.nspname NOT LIKE 'pg_toast_temp_%'
       AND c.relkind IN ('r','p','v','m','S','f')")"
  log "database ${PGDATABASE} exists; non-system object count=${OBJ_COUNT}"
  if [ "${OBJ_COUNT}" -gt 0 ]; then
    if drop_allowed; then
      log "DROP_DATABASE_IF_EXISTS=${DROP_DATABASE_IF_EXISTS}; dropping and recreating ${PGDATABASE} OWNER ${OWNER_ROLE}"
      psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d postgres \
        -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${PGDATABASE}' AND pid <> pg_backend_pid();" \
        -c "DROP DATABASE \"${PGDATABASE}\";" \
        -c "CREATE DATABASE \"${PGDATABASE}\" OWNER \"${OWNER_ROLE}\";"
    else
      log "ERROR: database ${PGDATABASE} exists and is not empty (${OBJ_COUNT} objects)" >&2
      log "ERROR: set DROP_DATABASE_IF_EXISTS=yes to allow drop+recreate, or restore into an empty DB" >&2
      mark_failed
      exit 1
    fi
  else
    log "database ${PGDATABASE} exists but is empty; setting OWNER ${OWNER_ROLE}"
    psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d postgres \
      -c "ALTER DATABASE \"${PGDATABASE}\" OWNER TO \"${OWNER_ROLE}\";"
  fi
else
  log "database ${PGDATABASE} does not exist; creating OWNER ${OWNER_ROLE}"
  psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d postgres \
    -c "CREATE DATABASE \"${PGDATABASE}\" OWNER \"${OWNER_ROLE}\";"
fi

log "ensuring roles referenced in dump exist (OWNER TO / GRANT / FOR ROLE)"
{
  grep -oE 'OWNER TO "[^"]+"' "$DUMP" || true
  grep -oE 'FOR ROLE "[^"]+"' "$DUMP" || true
  grep -oE ' TO "[^"]+";' "$DUMP" || true
  grep -oE ' FROM "[^"]+";' "$DUMP" || true
} | sed -E 's/^OWNER TO "//; s/^FOR ROLE "//; s/^ TO "//; s/^ FROM "//; s/";?$//' \
  | sort -u \
  | while IFS= read -r role; do
      ensure_role "$role" NOLOGIN
    done

log "applying pg_dump SQL into ${PGDATABASE}"
# Newer pg_dump may emit SQL/meta unknown to older psql clients:
# transaction_timeout (PG17+) and \restrict/\unrestrict (PG17+).
# pgAudit objects are optional (STRIP_PGAUDIT_EXTENSION); restore targets
# usually lack shared_preload_libraries=pgaudit.
FILTERED="${WORKDIR}/dump.filtered"
if strip_pgaudit_enabled \
  && { grep -Eq 'CREATE EXTENSION.*(pgaudit|"pgaudit")' "$DUMP" \
    || grep -Eq 'DROP EXTENSION.*(pgaudit|"pgaudit")' "$DUMP" \
    || grep -Eq 'EVENT TRIGGER[[:space:]]+"?pgaudit_' "$DUMP" \
    || grep -Eq 'FUNCTION[[:space:]].*pgaudit_(ddl_command_end|sql_drop)' "$DUMP"; }; then
  log "pgAudit objects in dump; STRIP_PGAUDIT_EXTENSION=${STRIP_PGAUDIT_EXTENSION}; stripping extension/event-trigger/function DDL"
fi
IS_TIMESCALE=0
if grep -Eq 'CREATE EXTENSION.*(timescaledb|"timescaledb")' "$DUMP" \
  || grep -Eq 'DROP EXTENSION.*(timescaledb|"timescaledb")' "$DUMP" \
  || grep -Eq '_timescaledb_catalog' "$DUMP"; then
  IS_TIMESCALE=1
fi

if [ "$IS_TIMESCALE" -eq 1 ]; then
  # Dumps use --clean, which DROP EXTENSION CASCADE mid-file and clears
  # timescaledb.restoring. Strip those lines; create the extension ourselves,
  # then pre_restore so chunk COPY does not assert on hypertable lookup.
  log "TimescaleDB dump detected; stripping extension DROP/CREATE and enabling restore mode"
  filter_dump "$FILTERED" 1
  rm -f "$DUMP"
  DUMP="$FILTERED"
  psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d "${PGDATABASE}" \
    -c "CREATE EXTENSION IF NOT EXISTS timescaledb;" \
    -c "SELECT public.timescaledb_pre_restore();"
  psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d "${PGDATABASE}" -f "$DUMP"
  log "timescaledb_post_restore"
  psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d "${PGDATABASE}" \
    -c "SELECT public.timescaledb_post_restore();"
else
  filter_dump "$FILTERED" 0
  rm -f "$DUMP"
  DUMP="$FILTERED"
  psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d "${PGDATABASE}" -f "$DUMP"
fi
log "psql restore finished"
log "database size summary"
psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d "${PGDATABASE}" -c "SELECT pg_size_pretty(pg_database_size(current_database()));"
log "table size summary"
psql -v ON_ERROR_STOP=1 -h "${PGHOST}" -U "${PGUSER}" -d "${PGDATABASE}" -c "
  SELECT
      schemaname,
      relname AS table_name,
      n_live_tup AS estimated_rows,
      pg_size_pretty(pg_total_relation_size(relid)) AS total_size,
      pg_size_pretty(pg_relation_size(relid)) AS table_size,
      pg_size_pretty(
          pg_total_relation_size(relid) - pg_relation_size(relid)
      ) AS index_size
  FROM pg_stat_user_tables
  ORDER BY pg_total_relation_size(relid) DESC;
"

rm -f "$DUMP" "${WORKDIR}/dump"*
touch "${WORKDIR}/.step-job-done"
log "wrote marker .step-job-done; releasing sibling containers"
# Peers poll every 5s; wait then drop this Job's scratch dir (no retention).
log "waiting for sibling containers to observe job-done before removing scratch"
sleep 20
log "removing restore scratch dir ${WORKDIR}"
rm -rf "${WORKDIR}"
log "stage done"
