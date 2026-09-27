set -eu
umask 0002
STAGE=cleanup
# Shared PVC is reused across Jobs; scope scratch to this pod
# so peers never see stale .step-* markers from a prior run.
# DATA_DIR is the PVC mount (engine-neutral). Legacy PGDUMP_DIR still accepted.
DATA_ROOT="${DATA_DIR:-${PGDUMP_DIR:?DATA_DIR or PGDUMP_DIR required}}"
DATA_DIR="${DATA_ROOT}/${HOSTNAME}"
pipeline_init
already_done_hold "${DATA_DIR}/.step-cleanup-done" "cleanup"
log "stage start: cleanup root=${DATA_ROOT}"
log "scratch dir=${DATA_DIR}"
log "clearing step markers under ${DATA_DIR}"
rm -f "${DATA_DIR}/.step-"*
LOCAL_KEEP="${LOCAL_RETENTION_DAYS:-7}"
RETAINED_DIR="${DATA_ROOT}/retained"
mkdir -p "${RETAINED_DIR}"
log "local retention=${LOCAL_KEEP} day(s); pruning retained dumps under ${RETAINED_DIR}"
prune_pipeline_logs "${DATA_ROOT}" "${LOCAL_KEEP}"
DUMP_PREFIX="${DUMP_PREFIX:-pg_dump}"
case "${DUMP_PREFIX}" in
  mysqldump) RETAINED_GLOB='mysqldump-*.sql.gz.gpg' ;;
  redisdump) RETAINED_GLOB='redisdump-*.rdb.gz.gpg' ;;
  pvcdump)   RETAINED_GLOB='pvcdump-*.tar.gz.gpg' ;;
  *)         RETAINED_GLOB='pg_dump-*.pgdump.gz.gpg' ;;
esac
find "${RETAINED_DIR}" -type f -name "${RETAINED_GLOB}" -mtime "+${LOCAL_KEEP}" -print \
  | while IFS= read -r f; do
      log "deleted expired local ${f}"
      rm -f "${f}"
    done || true
# Legacy / stray encrypted dumps outside retained/
# -delete implies -depth, which disables -prune; delete via rm instead.
find "${DATA_ROOT}" \( -path "${RETAINED_DIR}" -o -path "${DATA_DIR}" \) -prune -o \
  -type f \( -name 'pg_dump-*.pgdump.gz.gpg' -o -name 'mysqldump-*.sql.gz.gpg' -o -name 'redisdump-*.rdb.gz.gpg' -o -name 'pvcdump-*.tar.gz.gpg' \) \
  -mtime "+${LOCAL_KEEP}" -print \
  | while IFS= read -r f; do
      log "deleted expired local ${f}"
      rm -f "${f}"
    done || true
# Job scratch dirs ($HOSTNAME) are ephemeral once .gpg is in retained/.
# Drop every prior process dir immediately (keep retained/, logs/, lost+found).
log "removing old process/scratch dirs (keep retained/, logs/, and current ${HOSTNAME})"
find "${DATA_ROOT}" -mindepth 1 -maxdepth 1 -type d \
  ! -name "${HOSTNAME}" ! -name retained ! -name logs ! -name lost+found -print \
  | while IFS= read -r d; do
      log "removing old process dir ${d}"
      rm -rf "${d}"
    done || true
touch "${DATA_DIR}/.step-cleanup-done"
log "wrote marker .step-cleanup-done; stage work done"
hold_until_job_done
