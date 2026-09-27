set -eu
umask 0002
STAGE=pvcdump
# Shared PVC is reused across Jobs; scope scratch to this pod
# so peers never see stale .step-* markers from a prior run.
DATA_ROOT="${DATA_DIR:-${PGDUMP_DIR:?DATA_DIR or PGDUMP_DIR required}}"
DATA_DIR="${DATA_ROOT}/${HOSTNAME}"
pipeline_init
wait_for "${DATA_DIR}/.step-cleanup-done" "cleanup"
already_done_hold "${DATA_DIR}/.step-dump-done" "dump"
clear_step_failed
CLAIM="${PVC_CLAIM_NAME:?PVC_CLAIM_NAME required}"
MOUNT="${PVC_SOURCE_DIR:?PVC_SOURCE_DIR required}"
# PVC_PATH is relative to the claim root (validated: no leading / or ..).
SRC="${MOUNT}"
if [ -n "${PVC_PATH:-}" ]; then
  SRC="${MOUNT}/${PVC_PATH}"
fi
if [ ! -d "${SRC}" ]; then
  log "ERROR: source ${SRC} is not a directory (claim=${CLAIM} path=${PVC_PATH:-.})" >&2
  mark_failed
  exit 1
fi
log "stage start: archive claim=${CLAIM} path=${PVC_PATH:-.} uid=$(id -u) gid=$(id -g)"
log "scratch dir=${DATA_DIR}"
EXCLUDES="/tmp/pvcdump.excludes"
: > "${EXCLUDES}"
if [ -n "${PVC_EXCLUDES:-}" ]; then
  printf '%s\n' "${PVC_EXCLUDES}" > "${EXCLUDES}"
  while IFS= read -r pattern || [ -n "$pattern" ]; do
    [ -n "$pattern" ] || continue
    log "exclude ${pattern}"
  done < "${EXCLUDES}"
fi
OUT="${DATA_DIR}/pvcdump-${CLAIM}-$(date '+%Y-%m-%d-%H-%M').tar"
log "running tar -> ${OUT}"
err="${DATA_DIR}/.tar.stderr"
dump_heartbeat_start "${OUT}"
set +e
# GNU tar. Numeric owners only: the image's /etc/passwd says nothing about the
# app's users, and a later extract must not remap UIDs by name.
tar --create --file="${OUT}" --directory="${SRC}" \
  --numeric-owner --totals --warning=no-file-changed \
  --exclude-from="${EXCLUDES}" . 2>"${err}"
ec=$?
set -e
dump_heartbeat_stop
log_file_lines "tar: " "${err}"
rm -f "${err}"
# GNU tar exit 1: some files changed or vanished while being read. The archive
# is complete and readable, just not a point-in-time copy. 2+ is fatal.
if [ "$ec" -eq 1 ]; then
  log "WARNING: tar exit=1; files changed while archiving (archive kept, not point-in-time)"
elif [ "$ec" -ne 0 ]; then
  log "ERROR: tar failed exit=${ec}" >&2
  mark_failed
  exit "$ec"
fi
log "tar finished size=$(wc -c < "${OUT}") bytes"
log "WARNING: the source stays live during tar; quiesce the app for a consistent copy"
touch "${DATA_DIR}/.step-dump-done"
log "wrote marker .step-dump-done; stage work done"
hold_until_job_done
