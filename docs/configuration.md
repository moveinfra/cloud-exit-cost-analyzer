# Configuration

Set values with `--set` or a values file:

```sh
helm upgrade --install cost oci://ghcr.io/moveinfra/charts/cloud-exit-cost-analyzer \
  -n cost-analyzer --create-namespace -f my-values.yaml
```

Each Helm value becomes an environment variable of the container, so the binary can also be run without Helm.

| Helm value | Environment variable | Default | Meaning |
|---|---|---|---|
| `upcloud.zone` | `UPCLOUD_ZONE` | `de-fra1` | UpCloud zone to price. Must exist in the price data (15 zones, e.g. `fi-hel1`, `nl-ams1`, `se-sto1`, `pl-waw1`, `uk-lon1`). |
| `upcloud.planFamilies` | `PLAN_FAMILIES` (comma-separated) | `general,HICPU,HIMEM` | Server plan families for worker nodes. Also `PREMIUM`, `CLOUDNATIVE`, `DEV`, `STARTER`. |
| `estimate.minNodes` | `MIN_NODES` | `3` | Never plan fewer worker nodes than this. |
| `estimate.cpuHeadroom` | `CPU_HEADROOM` | `0.25` | Right-sized CPU = p95 use + 25%. |
| `estimate.memoryHeadroom` | `MEMORY_HEADROOM` | `0.15` | Right-sized memory = peak use + 15%. |
| `estimate.nodeReserve` | `NODE_RESERVE` | `0.1` | Share of each server kept free for the OS and kubelet. |
| `current.discount` | `CURRENT_DISCOUNT` | `0` | Your discount on compute list prices (0.2 = 20%, e.g. savings plans). |
| `current.priceSpotAsOnDemand` | `PRICE_SPOT_AS_ON_DEMAND` | `false` | Count spot nodes at on-demand prices instead of leaving them out. |
| `current.instancePrices` | `INSTANCE_PRICES` (JSON) | `{}` | Price per hour by instance type. Overrides the built-in list. |
| `sampleInterval` | `SAMPLE_INTERVAL` | `60s` | How often usage is read. Minimum 15 s. |
| `persistence.enabled` | — | `true` | Keep history on a PVC. Off = history restarts with the pod. |
| `persistence.size` | — | `1Gi` | PVC size. A few MB are used in practice. |
| `persistence.storageClass` | — | cluster default | Storage class for the PVC. |
| — | `DATA_DIR` | `/data` | Where `usage.json` is written. |
| — | `LISTEN_ADDR` | `:8080` | HTTP listen address. |
| `image.repository`, `image.tag` | — | `ghcr.io/moveinfra/cloud-exit-cost-analyzer`, chart appVersion | Image to run. |
| `resources`, `nodeSelector`, `tolerations`, `affinity`, `podAnnotations` | — | small requests | Usual pod settings. |

## Examples

GKE or DigitalOcean (no built-in prices yet), or negotiated prices:

```yaml
current:
  instancePrices:
    e2-standard-4: 0.155
    s-4vcpu-8gb: 0.0714
```

With `--set`, quote dotted instance types: `--set current.instancePrices.m5\.large=0.096`.

AWS with a savings plan, priced in Helsinki:

```yaml
upcloud:
  zone: fi-hel1
current:
  discount: 0.25
```

Bigger servers only:

```yaml
upcloud:
  planFamilies: [general]
estimate:
  minNodes: 2
```

## Opening the report

The service is `ClusterIP` on purpose: the report shows workload names and sizes. Use a port-forward:

```sh
kubectl -n cost-analyzer port-forward svc/cost-cloud-exit-cost-analyzer 8080
```

If you expose it through an ingress, put authentication in front of it.
