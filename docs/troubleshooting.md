# Troubleshooting

Logs are JSON on stdout: `kubectl -n cost-analyzer logs deploy/cost-cloud-exit-cost-analyzer`.

| Symptom | Cause and fix |
|---|---|
| Page says "collecting the first snapshot" | The first sample has not finished. Wait a few seconds. If it stays, check the logs for `snapshot` errors (usually RBAC). |
| "No usage data yet" | metrics-server is missing or not ready. `kubectl top pods -A` must work. Logs show `usage … metrics.k8s.io`. |
| Usage columns stay "–" for a workload | Fewer samples than needed, or the workload has no running pods. Usage-based suggestions start after 60 samples (1 hour at 60 s). |
| Today's cost is 0 or very low | The region or instance types are not in the built-in prices (GKE, DigitalOcean, other regions), or nodes are spot. See "Not included" in the report; set `current.instancePrices` or `current.priceSpotAsOnDemand`. |
| `no UpCloud prices for zone "…"` and the pod exits | `upcloud.zone` is not in the price data. The error lists the known zones. |
| `INSTANCE_PRICES must be a JSON object …` | `current.instancePrices` is not a map of instance type → number. |
| A scenario shows "no UpCloud plan fits the largest pod" | One pod requests more than the biggest allowed plan. Add plan families (`upcloud.planFamilies`), e.g. `HIMEM`. |
| History is lost after a restart | `persistence.enabled` is false, or the PVC could not bind (no default storage class). Check `kubectl -n cost-analyzer get pvc`. |
| Pod is `Forbidden` listing something | The ClusterRole was changed or not installed. Reinstall the chart; it needs cluster-wide get/list on nodes, pods, services, persistentvolumes, storageclasses and metrics. |
| Numbers differ from my bill | The report uses list prices and only sees what runs in Kubernetes. Discounts, data transfer, support plans and services outside the cluster are not included. Use `current.discount` for a flat discount. |

Still stuck? Open an issue with the analyzer version, Kubernetes version, cloud and region, and the relevant log lines.
Do not paste your full report; it lists your workload names.
