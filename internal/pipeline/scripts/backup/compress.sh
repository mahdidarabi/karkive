set -eu
umask 0002
STAGE=compress
# Shared PVC is reused across Jobs; scope scratch to this pod
# so peers never see stale .step-* markers from a prior run.
DATA_ROOT="${DATA_DIR:-${PGDUMP_DIR:?DATA_DIR or PGDUMP_DIR required}}"
DATA_DIR="${DATA_ROOT}/${HOSTNAME}"
pipeline_init
wait_for "${DATA_DIR}/.step-dump-done" "dump"
already_done_hold "${DATA_DIR}/.step-compress-done" "compress"
log "stage start: compress"
DUMP_PREFIX="${DUMP_PREFIX:-pg_dump}"
case "${DUMP_PREFIX}" in
  mysqldump) PLAIN_GLOB='mysqldump-*.sql' ;;
  redisdump) PLAIN_GLOB='redisdump-*.rdb' ;;
  pvcdump)   PLAIN_GLOB='pvcdump-*.tar' ;;
  *)         PLAIN_GLOB='pg_dump-*.pgdump' ;;
esac
ls "${DATA_DIR}"/${PLAIN_GLOB} >/dev/null 2>&1 || {
  log "ERROR: no ${PLAIN_GLOB} after dump marker" >&2
  mark_failed
  exit 1
}
found=0
for f in "${DATA_DIR}"/${PLAIN_GLOB}; do
  [ -f "$f" ] || continue
  found=1
  log "gzip ${f} ($(wc -c < "$f") bytes)"
  gzip "$f"
  log "gzip done -> ${f}.gz ($(wc -c < "${f}.gz") bytes)"
done
if [ "$found" -eq 0 ]; then
  log "ERROR: no ${PLAIN_GLOB} files to compress" >&2
  mark_failed
  exit 1
fi
touch "${DATA_DIR}/.step-compress-done"
log "wrote marker .step-compress-done; stage work done"
hold_until_job_done
