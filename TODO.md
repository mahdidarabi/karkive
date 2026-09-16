# TODO

## Operator skeleton

- [x] Go module and project layout (`cmd`, `api`, `internal`, Helm)
- [x] `Backup` and `Restore` CRDs (`karkive.io/v1alpha1`)
- [x] Engine enum: `postgres` / `mariadb` / `redis`
- [x] Generate deepcopy and CRD/RBAC manifests (`make generate manifests`)
- [x] Owned resource prefix: `karkive-<cr-name>`
- [x] `app.kubernetes.io/component` from `spec.component` (defaults to CR name)
- [x] Operator does not create Secrets; it only reads `spec.secretRef`
- [x] Default image and S3 flags plus `WATCH_NAMESPACE`
- [x] Helm chart to deploy the operator
- [x] Dockerfile, Makefile, samples, unit tests

## Postgres backup

- [x] Pipeline scripts: `cleanup` → `pgdump` → `compress` → `encrypt` → `s3-sync`
- [x] Backup controller: ConfigMap + optional PVC + CronJob
- [x] Split images: busybox, `vladgh/gpg`, CNPG postgres, minio/mc
- [x] PVC or emptyDir via `spec.persistence.enabled`
- [x] Secret keys: `username`, `password`, `s3_access_key`, `s3_secret_key`, `gpg_passphrase`
- [x] Status: `Ready` / `Pending` / `Error` / `Unsupported`
- [x] Backup samples and examples



## Postgres restore

- [x] Pipeline scripts: `cleanup` → `fetch` → `decrypt` → `extract` → `pgrestore`
- [x] Restore controller: ConfigMap + optional PVC + CronJob
- [x] `secretRef` is S3+GPG only; target DB credentials come from `spec.postgresSecret`
- [x] Job defaults: `restartPolicy: Never`, `backoffLimit: 1`
- [x] `useLatestBackupAsFallback` / `dropDatabaseIfExists` / `stripPgAuditExtension`
- [x] Restore samples and examples



## Monitoring

- [x] Operator `/healthz` and `/readyz`
- [x] Helm liveness and readiness probes
- [x] controller-runtime metrics bind address (`:8080`)
- [x] Metrics Service for the operator
- [x] Prometheus `ServiceMonitor`
- [x] Custom metrics per Backup/Restore (last success, duration, failures)
- [x] Alerts for missed schedules and failed Jobs
- [x] Grafana dashboard



## MariaDB

- [x] Pipeline scripts: `cleanup` → `mysqldump` → `compress` → `encrypt` → `s3-sync`
- [x] Restore: `cleanup` → `fetch` → `decrypt` → `extract` → `mysqlrestore`
- [x] `spec.mariadbSecret` for target DB credentials



## Redis

- [x] Backup: `redis-cli --rdb`
- [x] Restore: load RDB into an ephemeral `redis-server` and `REPLICAOF` into the target. Non-empty targets require `dropDatabaseIfExists`.
- [x] `spec.redisSecret` for target credentials



## Later (done)

- [x] Validating webhooks for Backup/Restore
- [x] CI (tests + image build)
- [x] Publish the operator image to GHCR
- [x] Publish the Helm chart
- [x] Richer status from the last Job (success/failure, failure reason)



## Pipeline

- [x] DRY bash: extract shared `log` / `wait_for` / `hold_until_job_done` / `mark_failed` into one sourced helper (embed `common.sh` for backup and restore)
- [x] MariaDB dump/restore: `--hex-blob` / utf8mb4 / `--databases`; restore strips GTID/DEFINER and recreates with utf8mb4
- [x] Redis restore: ephemeral `redis-server` + `REPLICAOF` (not SCAN+MIGRATE)
- [x] Make S3 sync optional (`spec.s3.enabled`, default true). When false: skip the `s3-sync` container, do not require S3 secret keys or endpoint/bucket. Encrypt still writes `retained/` on the PVC.
- [ ] Restore from local retained dumps (`spec.source: s3 | pvc`) when Backup skipped S3
- [ ] Pick latest restore object by `mc find --json` `lastModified`, not `sort | tail`. Optionally add seconds + a unique suffix to dump filenames
- [ ] Redis `REPLICAOF` restore needs the target to connect inbound to the Job pod `:6380` (NetworkPolicy / firewall). Document that; if replicaof is blocked, add a SCAN+MIGRATE fallback that pushes from the Job



## API and controller

- [x] Keep `Ready` = resources admitted/synced. Add `BackupSucceeded` / `RestoreSucceeded` conditions from the last finished Job (do not overload `status.phase`)
- [x] Prefix owned names by kind: `karkive-backup-<name>` and `karkive-restore-<name>` (breaking: migrate or document rename). Alternative: webhook uniqueness across kinds — weaker, still share one CronJob name if both CRs are `app-postgres`
- [x] Event only on create or spec change. Patch status only when phase, conditions, or `lastJob` actually change
- [x] One owned-resource helper parameterized by kind. One `ImageSet` type instead of `BackupImages` + `RestoreImages`
- [ ] Reject `spec.job.restartPolicy: Always` (Jobs cannot use it). Default Never for backup and restore
- [ ] Restore `spec.schedule` optional. Empty → create a suspended CronJob for `kubectl create job --from=…`
- [ ] Default `dropDatabaseIfExists` to false (no implicit dataset replace / DROP DATABASE). Destructive restore must be explicit
- [ ] Watch Secrets (`secretRef` + restore target secret) instead of 30s requeue on NotFound



## Security and engines

- [ ] FSGroup per engine, or one shared GID with matching `runAsGroup` (today always Postgres UID 26)
- [ ] Postgres restore: do not interpolate `${PGDATABASE}` / `${role}` into SQL. Strict `[A-Za-z0-9_]+` or `psql` variables / `quote_ident`



## Features

- [x] Optional `spec.runtime.mode: CronJobWithVolumeHolder`: pause Deployment holds the PVC attached; CronJob pods use required podAffinity on `kubernetes.io/hostname`. Default remains `CronJob`.
- [ ] `spec.runtime.mode: PersistentPodWithTriggerJob`. Notes: long-lived pipeline Deployment keeps the PVC mounted; PVC-less trigger CronJob writes a run-token ConfigMap and waits on a result ConfigMap (keeps Job status / `kubectl create job --from=`). Scratch must be `$DATA_ROOT/run-$RUN_ID` (HOSTNAME is stable; `already_done_hold` would skip later runs). Stages must loop on the token and must not exit after `.step-job-done` (Deployment restart would skip work). Role limited to those ConfigMaps. Idle memory is all stage requests — prefer VolumeHolder for rare restores.
- [ ] `spec.runtime.mode: PersistentPodWithInPodCron`. Notes: same long-lived pipeline pod as the trigger-Job mode, but a sidecar (e.g. supercronic) fires the run token. No CronJob; manual trigger is annotate/exec; last-run status cannot use Job objects unless a result ConfigMap is added. Implement trigger-Job mode first and share the loop/scratch protocol.
- [ ] Pin the volume-holder Deployment to the bound node without recreating the pod after attach. Notes: adding nodeAffinity after the holder is Running changes the pod template (Recreate → detach). Freeze affinity on first create only, or pin from `volume.kubernetes.io/selected-node` before the holder exists. Do not set `nodeName` on a live template.
- [ ] `spec.backupRef` on Restore: inherit engine, S3 path, and encryption settings from a Backup
- [ ] BackupRun / manual trigger CR or subresource (no `kubectl create job --from=cronjob/…`)
- [ ] Last object in status: S3 key, size, checksum of the last successful dump; Restore can default to it
- [ ] Optional TLS for DB connections (`sslmode` / CA Secret for Postgres and MariaDB; Redis TLS)
- [ ] Dump extras: `spec.database.extraArgs`, `excludeTables`, schema-only / data-only
- [ ] Optional restore-verification Job after backup (ephemeral target; fail Backup on checksum / restore error)
- [ ] Optional public-key GPG (encrypt to a public key Secret; private key only on Restore)
- [ ] envtest for CronJob apply; shellcheck + bats for pipeline scripts