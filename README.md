# KArkive

[![Website](https://img.shields.io/badge/website-karkive.ir-c4a35a)](https://karkive.ir)
[![CI](https://github.com/mahdidarabi/karkive/actions/workflows/ci.yaml/badge.svg)](https://github.com/mahdidarabi/karkive/actions/workflows/ci.yaml)
[![Helm](https://img.shields.io/badge/Helm-0.0.11--p.2-0F1689?logo=helm)](https://github.com/mahdidarabi/karkive/pkgs/container/charts%2Fkarkive)
[![Image](https://img.shields.io/badge/GHCR-karkive-blue?logo=github)](https://github.com/mahdidarabi/karkive/pkgs/container/karkive)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
[![Kubebuilder](https://img.shields.io/badge/API-karkive.io%2Fv1alpha1-326CE5?logo=kubernetes)](https://github.com/mahdidarabi/karkive/tree/main/api/v1alpha1)

Site: **[karkive.ir](https://karkive.ir)**

Kubernetes operator for **scheduled logical backups and restores**. You declare a `Backup` or `Restore` CR; KArkive owns a ConfigMap, optional PVC, and CronJob that dump, gzip, GPG-encrypt, and optionally sync to S3 (and the reverse).

**Engines:** PostgreSQL · MariaDB · Redis

The operator never creates Secrets. It only reads `spec.secretRef` (and the engine-specific restore secret) from the same namespace as the CR.

```text
kubectl get backups,restores
# short names: kbackup / bak, krestore / res
```

---

## Contents

- [How it works](#how-it-works)
- [Install](#install)
  - [Upgrade from 0.0.3](#upgrade-from-003)
- [Backup](#backup)
- [Restore](#restore)
- [Secrets](#secrets)
- [Status](#status)
- [Monitoring](#monitoring)
- [Webhooks](#webhooks)
- [Configuration](#configuration)
- [Development](#development)
- [Layout](#layout)

## How it works

A CR named `app-postgres` is reconciled into owned resources prefixed by kind:

| Resource | Backup | Restore | Notes |
| --- | --- | --- | --- |
| ConfigMap | `karkive-backup-app-postgres` | `karkive-restore-app-postgres` | Embedded pipeline scripts |
| PVC | `karkive-backup-app-postgres` | `karkive-restore-app-postgres` | Skip with `spec.persistence.enabled: false` (emptyDir) |
| CronJob | `karkive-backup-app-postgres` | `karkive-restore-app-postgres` | One Job per schedule (or `kubectl create job --from=…`) |
| Deployment | `karkive-backup-app-postgres-holder` | `karkive-restore-app-postgres-holder` | Only when `spec.runtime.mode: CronJobWithVolumeHolder` |

```mermaid
flowchart LR
  CR["Backup / Restore CR"] --> OP[KArkive operator]
  OP --> CM[ConfigMap]
  OP --> PVC[PVC or emptyDir]
  OP --> CJ[CronJob]
  CJ --> P[Pipeline Job]
  P --> S3[(S3)]
  P --> DB[(Postgres / MariaDB / Redis)]
```

### Backup pipeline

`cleanup` → dump → `compress` → `encrypt` → `s3-sync` (omit `s3-sync` when `spec.s3.enabled` is false)

### Restore pipeline

`cleanup` → `fetch` → `decrypt` → `extract` → restore

| Engine | Dump | Restore |
| --- | --- | --- |
| `postgres` | `pg_dump` | `pgrestore` |
| `mariadb` | `mysqldump` | `mysqlrestore` |
| `redis` | `redis-cli --rdb` | ephemeral `redis-server` + `REPLICAOF` |

Shared stages use BusyBox (`find` / `gzip`), `vladgh/gpg`, and MinIO `mc`. Engine images default to CloudNativePG PostgreSQL 18.4, MariaDB 10.6, and Redis 7.4.

### Runtime

`spec.runtime.mode` (default `CronJob`) selects how pipeline pods are scheduled relative to the PVC:

| Mode | Status |
| --- | --- |
| `CronJob` | Implemented. New Job pod each run (CSI attach/detach). |
| `CronJobWithVolumeHolder` | Implemented. Pause Deployment keeps the PVC attached on one node; Job pods colocate with required podAffinity. Requires `spec.persistence.enabled`. Ready waits until the holder is Available. |
| `PersistentPodWithTriggerJob` | Not implemented (admission rejects). |
| `PersistentPodWithInPodCron` | Not implemented (admission rejects). |

`CronJobWithVolumeHolder` is for clusters where a new Job cannot attach the claim after the previous pod is gone (not RWX). RWO still allows several pods on **one node**; the holder is the long-lived attacher, the Job only mounts.

```yaml
spec:
  runtime:
    mode: CronJobWithVolumeHolder
  persistence:
    enabled: true
```

Redis restore starts an ephemeral `redis-server` in the Job and has the target `REPLICAOF` that pod (`:6380`). The target must be able to connect to the Job pod. A non-empty target is replaced only when `spec.dropDatabaseIfExists` is true (the default).

## Install

Images are published to `ghcr.io/mahdidarabi/karkive` from GitHub Actions on `main` (`latest`, `main`, `sha-<git-sha>`) and on tags `v*` (semver). Helm charts are pushed to GHCR on tags `v*`. `Chart.yaml` `version` and `appVersion` must match the tag without the `v` prefix.

Current release: **`0.0.11-p.3`**

```bash
helm show chart oci://ghcr.io/mahdidarabi/charts/karkive --version 0.0.11-p.3

helm install karkive oci://ghcr.io/mahdidarabi/charts/karkive \
  --version 0.0.11-p.3 \
  -n karkive-system --create-namespace
```

With Prometheus Operator scrape, alerts, and a Grafana dashboard ConfigMap:

```bash
helm install karkive oci://ghcr.io/mahdidarabi/charts/karkive \
  --version 0.0.11-p.3 \
  -n karkive-system --create-namespace \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.prometheusRule.enabled=true \
  --set metrics.grafanaDashboard.enabled=true
```

On GitOps (Argo CD), prefer cert-manager for webhook serving certs so Helm does not regenerate a CA on every template:

```bash
helm install karkive oci://ghcr.io/mahdidarabi/charts/karkive \
  --version 0.0.11-p.3 \
  -n karkive-system --create-namespace \
  --set webhook.certManager.enabled=true
```

cert-manager must already be installed. If you previously used the Helm-generated Secret, delete it once so cert-manager can create it.

The chart applies Backup and Restore CRDs (`crds.install`, default true) so `helm upgrade` updates the schema. Uninstall keeps the CRDs.

### Upgrade from 0.0.3

**Breaking:** owned names are now `karkive-backup-<cr-name>` and `karkive-restore-<cr-name>` so a Backup and Restore can share a name (for example both `app-postgres`) without colliding on one CronJob.

On reconcile the operator creates the new ConfigMap/CronJob/PVC and **deletes** the old `karkive-<cr-name>` ConfigMap and CronJob when this CR still owns them. Manual Jobs must use the new CronJob name:

```bash
kubectl create job --from=cronjob/karkive-backup-app-postgres app-postgres-manual -n backup
```

PVCs cannot be renamed. The old `karkive-<cr-name>` PVC is left in place (retained dumps stay there). Copy anything you still need onto the new PVC, then delete the leftover claim. S3 objects are unchanged.

## Backup

```yaml
apiVersion: karkive.io/v1alpha1
kind: Backup
metadata:
  name: app-postgres
  namespace: backup
spec:
  engine: postgres          # postgres | mariadb | redis
  schedule: "0 2 * * *"
  database:
    host: postgres.example.svc.cluster.local
    port: 5432              # defaults: 5432 / 3306 / 6379
    name: app
  s3:
    enabled: true           # false → skip s3-sync; dumps stay in retained/ on the PVC
    endpoint: https://s3.example.com
    bucket: backups
    path: app/pgdump
    retentionDays: 14       # S3 object retention (default 14)
  secretRef:
    name: backup-app-postgres
  persistence:
    enabled: true           # false → emptyDir
    size: 1Gi
    storageClassName: standard
  localRetentionDays: 7     # encrypted dumps kept on the PVC
  logFileEnabled: false      # true → also write logs/<pod>.log on the volume
```

MariaDB and Redis samples live under [`examples/`](examples/) (`backup-mariadb.yaml`, `backup-redis.yaml`).

Useful knobs:

| Field | Default | Purpose |
| --- | --- | --- |
| `spec.s3.enabled` | `true` | Upload to S3. `false` keeps dumps in `retained/` on the PVC (requires persistence; no S3 keys) |
| `spec.logFileEnabled` | `false` | Also write stage logs to `logs/<pod>.log` on the volume |
| `spec.suspend` | `false` | Stop the schedule; CronJob remains for manual Jobs |
| `spec.job.concurrencyPolicy` | `Forbid` | CronJob concurrency |
| `spec.job.restartPolicy` | `Never` | Do not restart a single container in-place (wipes pipeline markers) |
| `spec.job.backoffLimit` | `3` | Job retries (new pod) |
| `spec.job.ttlSecondsAfterFinished` | `86400` | Cleanup finished Jobs |
| `spec.runtime.mode` | `CronJob` | `CronJob` (default) or `CronJobWithVolumeHolder`. `PersistentPodWithTriggerJob` and `PersistentPodWithInPodCron` are reserved and rejected until implemented |
| `spec.images` | operator defaults | Shared `ImageSet`: busybox / gpg / postgres / mariadb / redis / mc |
| `spec.resources` | — | Per-stage CPU/memory (`cleanup`, `dump`, `compress`, `encrypt`, `s3Sync`) |
| `spec.component` | CR name | `app.kubernetes.io/component` label |

Trigger a run without waiting for cron:

```bash
kubectl create job --from=cronjob/karkive-backup-app-postgres app-postgres-manual -n backup
```

## Restore

Restore `secretRef` is S3 + GPG only. Target database credentials come from `spec.postgresSecret`, `spec.mariadbSecret`, or `spec.redisSecret`.

```yaml
apiVersion: karkive.io/v1alpha1
kind: Restore
metadata:
  name: app-postgres
  namespace: backup
spec:
  engine: postgres
  schedule: "30 2 * * *"
  suspend: true             # typical: run on demand
  database:
    host: postgres.example.svc.cluster.local
    port: 5432
    name: app
    ownerRole: app
  s3:
    endpoint: https://s3.example.com
    bucket: backups
    path: app/pgdump
  secretRef:
    name: restore-app-postgres
  postgresSecret:
    name: postgres
    usernameKey: username
    passwordKey: password
  persistence:
    enabled: false
    size: 2Gi
  useLatestBackupAsFallback: true
  dropDatabaseIfExists: true
  stripPgAuditExtension: true
```

| Field | Default | Purpose |
| --- | --- | --- |
| `spec.backupFile` | empty | Specific S3 object name |
| `spec.useLatestBackupAsFallback` | `true` | Newest dump matching the engine prefix when `backupFile` is empty or missing |
| `spec.dropDatabaseIfExists` | `true` | Drop / replace a non-empty target first (`REPLICAOF` on Redis) |
| `spec.stripPgAuditExtension` | `true` | Strip pgAudit extension, event-trigger, and function DDL from Postgres dumps |
| `spec.logFileEnabled` | `false` | Also write stage logs to `logs/<pod>.log` on the volume |
| `spec.job.restartPolicy` | `Never` | Restore Jobs do not restart in-place |
| `spec.job.backoffLimit` | `1` | Fail fast |
| `spec.runtime.mode` | `CronJob` | Same modes as Backup. VolumeHolder requires persistence |

## Secrets

The operator does not create or mutate Secrets. Apply them yourself in the CR namespace.

**Backup** (`spec.secretRef`) needs:

| Key | Used for |
| --- | --- |
| `username` | DB user (Redis ACL user; `default` if unused) |
| `password` | DB password |
| `s3_access_key` | S3 (omit when `spec.s3.enabled` is false) |
| `s3_secret_key` | S3 (omit when `spec.s3.enabled` is false) |
| `gpg_passphrase` | Symmetric encryption of the dump |

**Restore** splits credentials:

| Source | Keys |
| --- | --- |
| `spec.secretRef` | `s3_access_key`, `s3_secret_key`, `gpg_passphrase` |
| `spec.postgresSecret` / `mariadbSecret` / `redisSecret` | `username` / `password` by default (`usernameKey` / `passwordKey` override) |

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: backup-app-postgres
  namespace: backup
type: Opaque
stringData:
  username: app
  password: change-me
  s3_access_key: change-me
  s3_secret_key: change-me
  gpg_passphrase: change-me
```

See [`config/samples/backup-secret.yaml`](config/samples/backup-secret.yaml).

## Status

`status.phase` and the **Ready** condition are only admission of owned resources (`Pending` · `Ready` · `Error` · `Unsupported`). They stay `Ready` while the CronJob is synced even if the last dump failed.

Last Job outcome is **BackupSucceeded** / **RestoreSucceeded** (`True` / `False` / `Unknown` until a Job finishes) plus `status.lastJob`:

```yaml
status:
  phase: Ready
  cronJobName: karkive-backup-app-postgres
  lastScheduleTime: "2026-08-20T02:00:00Z"
  lastSuccessfulTime: "2026-08-20T02:04:12Z"
  lastJob:
    name: karkive-backup-app-postgres-29781240
    outcome: Succeeded   # or Failed
    reason: DeadlineExceeded
    message: "Job was active longer than specified deadline"
  conditions:
    - type: Ready
      status: "True"
      reason: Synced
    - type: BackupSucceeded
      status: "True"      # False if last Job failed; Unknown if none has finished
      reason: JobSucceeded
```

```bash
kubectl get bak,res -A
kubectl describe backup app-postgres -n backup
```

Print columns include engine, schedule, phase, Succeeded, last success (Backup), and last Job outcome.

## Monitoring

The chart exposes `/metrics` on a ClusterIP Service (`:8080`) and `/healthz` + `/readyz` on `:8081`.

Custom series are labeled `namespace`, `name`, `engine`:

| Metric | Meaning |
| --- | --- |
| `karkive_backup_ready` / `karkive_restore_ready` | `1` when phase is `Ready` (resources synced; not last Job success) |
| `karkive_backup_suspended` / `karkive_restore_suspended` | `spec.suspend` |
| `karkive_backup_last_successful_timestamp_seconds` | Last successful Job |
| `karkive_backup_last_job_failed` | Last finished Job failed |
| `karkive_backup_last_job_duration_seconds` | Wall time of last Job |
| `karkive_backup_last_job_info` | Always `1`; labels `outcome`, `reason`, `job_name` |

Alerts (when `metrics.prometheusRule.enabled=true`):

- CR not Ready for 15m
- Last Job failed
- Backup last success older than `backupAgingSeconds` (default 8h) and not yet stale
- No successful backup within `backupStaleSeconds` (default 36h) on non-suspended Backups
- CronJob has scheduled but no Job has finished
- Missed CronJob schedule via kube-state-metrics (`karkive-backup-*` / `karkive-restore-*`)

## Webhooks

Validating admission webhooks reject invalid `Backup` / `Restore` specs (cron schedule, required fields, engine-specific restore secrets). They are **on** in the Helm chart and **off** for `make run`.

Serving certs default to Helm `genCA`. That Secret changes on every template, which fights GitOps. Set `webhook.certManager.enabled=true` instead.

## Configuration

Operator-wide defaults (Helm `values.yaml` → flags):

| Helm | Flag | Default |
| --- | --- | --- |
| `watchNamespace` | `WATCH_NAMESPACE` | all namespaces |
| `leaderElection` | `--leader-elect` | `true` in the chart |
| `defaults.busyboxImage` | `--busybox-image` | `busybox:1.37` |
| `defaults.gnupgImage` | `--gnupg-image` | `vladgh/gpg:1.3.11` |
| `defaults.postgresImage` | `--postgres-image` | `cloudnative-pg/postgresql:18.4` |
| `defaults.mariadbImage` | `--mariadb-image` | `mariadb:10.6` |
| `defaults.redisImage` | `--redis-image` | `redis:7.4` |
| `defaults.mcImage` | `--mc-image` | `minio/mc:RELEASE.2025-08-13T08-35-41Z` |
| `defaults.s3.endpoint` | `--default-s3-endpoint` | empty (required on the CR when S3 is enabled, unless set) |
| `defaults.s3.bucket` | `--default-s3-bucket` | empty |

Per-CR `spec.s3.endpoint` / `spec.s3.bucket` override the operator defaults. `spec.s3.path` is required when `spec.s3.enabled` is true (the default).

## Development

Requires Go (see `go.mod`) and a kubeconfig.

```bash
make generate manifests test
make run                    # against the current cluster; webhooks off

kubectl apply -f config/crd/bases
kubectl apply -f config/samples/backup-secret.yaml
kubectl apply -f config/samples/karkive_v1alpha1_backup.yaml
```

| Target | What it does |
| --- | --- |
| `make generate` | Deepcopy for `api/` |
| `make manifests` | CRDs, RBAC, webhook manifests |
| `make test` | `go test ./...` |
| `make docker-build` | Operator image (`IMG`, default `ghcr.io/mahdidarabi/karkive:dev`) |
| `make install` / `uninstall` | Apply / delete CRDs |

CI on `main` and tags `v*` runs generate/vet/test (failing on dirty git), builds `linux/amd64`, and on tags pushes the Helm chart to `oci://ghcr.io/mahdidarabi/charts`.

## Layout

```text
api/v1alpha1/            Backup + Restore types (karkive.io)
cmd/                     Operator entrypoint
internal/
  controller/            Reconcile loops + last-Job status
  resources/             ConfigMap / PVC / CronJob builders
  pipeline/scripts/      Embedded shell stages
  metrics/               Prometheus collector
  config/                Operator-wide image / S3 defaults
  jobstatus/             Job Complete / Failed parsing
charts/karkive/          Helm chart (CRDs, RBAC, webhook, metrics)
docs/                    Landing page (GitHub Pages → karkive.ir)
config/crd/bases/        Generated CRDs
config/samples/          Example Backup / Restore / Secret
examples/                Postgres, MariaDB, Redis CRs
```
