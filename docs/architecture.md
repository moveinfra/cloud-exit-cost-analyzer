# How it works

The analyzer is one Go binary running as a single pod. It reads the cluster, keeps a compact usage history, and
serves a report. It never writes to the cluster and never sends data anywhere.

```
            Kubernetes API (read-only)          metrics-server
                     │                                │
                     ▼                                ▼
            ┌──────────────────┐   every 60 s   ┌──────────────┐
            │ collect.Snapshot │ ─────────────▶ │ collect.Usage │
            └────────┬─────────┘                └──────┬───────┘
                     │ nodes, workloads,                │ CPU + memory per workload
                     │ volumes, load balancers          ▼
                     │                         ┌─────────────────┐  every 5 min   /data/usage.json
                     │                         │  usage.Store    │ ─────────────▶ (PVC)
                     │                         └────────┬────────┘
                     ▼                                  ▼
            ┌───────────────────────────────────────────────────┐
            │ estimate.Build  (+ pricing.Book, embedded prices) │
            └────────────────────────┬──────────────────────────┘
                                     ▼
                       web: /  /report.html  /api/report
```

## Packages

| Package | Job |
|---|---|
| `cmd/analyzer` | Reads configuration from environment variables, starts the sampling loop and the HTTP server, saves history on shutdown. |
| `internal/collect` | Lists nodes, pods, persistent volumes, storage classes and services. Groups pods into workloads (Deployment, StatefulSet, DaemonSet, CronJob, Job, bare Pod). Reads pod usage from `metrics.k8s.io`. |
| `internal/usage` | Keeps usage per workload as log-scale histograms (5% buckets), so weeks of one-minute samples fit in a few MB. Also keeps a 7-day cluster trend at 5-minute resolution. Saves to and loads from `DATA_DIR/usage.json`. |
| `internal/pricing` | Price lists for AWS, Azure, UpCloud and ECB exchange rates, embedded at build time from `internal/pricing/data/*.json`. |
| `internal/estimate` | Turns a snapshot plus history into the report: today's cost, three UpCloud scenarios, recommendations. See [estimates.md](estimates.md). |
| `internal/web` | Renders the report (one self-contained HTML file) and the JSON API. |
| `tools/pricegen` | Regenerates the price lists from the providers' APIs. Not part of the image. |

## What it reads

| Resource | Why | Verbs |
|---|---|---|
| `nodes` | instance type, region, capacity, spot or serverless | get, list |
| `pods` | requests per workload, which workload a pod belongs to | get, list |
| `persistentvolumes`, `storageclasses` | volume sizes and types (gp3, Premium_LRS, maxiops, …) | get, list |
| `services` | count of `type: LoadBalancer` | get, list |
| `metrics.k8s.io` pods, nodes | current CPU and memory use | get, list |

That is the whole ClusterRole (`charts/cloud-exit-cost-analyzer/templates/rbac.yaml`). No secrets, no config maps,
no write verbs.

## How a workload is identified

Usage is tracked per workload, not per pod, so restarts and rollouts keep their history:

- A pod owned by a ReplicaSet with a `pod-template-hash` label counts as its **Deployment**.
- A pod owned by a Job whose name ends in a scheduled timestamp counts as its **CronJob**.
- Anything else counts as its controller (StatefulSet, DaemonSet, Job) or, without one, as a bare **Pod**.

The key is `namespace/Kind/name`. Workloads not seen for 14 days are dropped from the history.

## Lifecycle

1. On start: load `usage.json` if it exists, check that the price data has the configured UpCloud zone.
2. Every `SAMPLE_INTERVAL` (default 60 s): take a snapshot, read usage, add it to the history.
3. Every 5 minutes and on `SIGTERM`: prune and save the history (atomic write via a temp file).
4. On every page request: build the report from the latest snapshot. Nothing is cached, so the page is always current.

## HTTP endpoints

| Path | Returns |
|---|---|
| `/` | the report as an HTML page |
| `/report.html` | the same page as a download |
| `/api/report` | the report as JSON |
| `/healthz` | `ok` (used by the liveness and readiness probes) |
