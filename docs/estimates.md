# How the estimate is calculated

All monthly figures use 730 hours (365 × 24 / 12), except UpCloud plans, which come from UpCloud's monthly price.
Prices are list prices without VAT. Current-cloud prices are converted to EUR with the ECB reference rate embedded
in the release.

## Today's cost

| Line | How |
|---|---|
| Nodes | instance type × on-demand hourly price for the node's region × 730. Linux prices. Multiplied by `1 − current.discount`. |
| Control plane | EKS: per-cluster hourly fee. AKS: Standard tier fee (Free tier clusters pay nothing; the report says so). |
| Volumes | each persistent volume's size × the price per GB-month of its type (AWS gp3, gp2, io1, …). |
| Load balancers | count of `Service type: LoadBalancer` × the hourly price of a Network Load Balancer. Traffic (LCU) is not included. |
| Already on UpCloud | nodes have no plan label, so each is priced as the cheapest allowed plan with the same vCPU and memory, marked "matched by size". |

**Not included**, listed in the report so the total reads as a lower bound: spot nodes (unless
`current.priceSpotAsOnDemand`), serverless nodes (Fargate, virtual nodes), instance types missing from the price
list, volumes of an unknown type, data transfer, backups, snapshots, NAT gateways and anything outside Kubernetes.

## The three UpCloud scenarios

All three add the UpCloud Managed Kubernetes control plane, MaxIOPS block storage and Managed Load Balancers.
Only plans in `upcloud.planFamilies` are used (default: General Purpose, HICPU, HIMEM).

### 1. Same servers
Every node becomes the cheapest plan with at least the node's vCPU and memory (5% tolerance on memory, because
instances report slightly less than their nominal size). No app changes. Includes all volumes and every load balancer.

### 2. Current requests
What the scheduler needs today, packed onto the cheapest set of identical servers:

- demand = sum of pod requests (the larger of the app containers' sum and the largest init container, plus pod overhead);
- DaemonSet requests are counted on every node instead of once;
- each server keeps `estimate.nodeReserve` (10%) free for the OS and kubelet;
- the largest single pod must fit on one server;
- at least `estimate.minNodes` (3) servers.

For every allowed plan it computes the node count needed for CPU and for memory, takes the larger, and keeps the
cheapest result. Only bound volumes are counted.

### 3. Right-sized
Same packing, with each workload's requests replaced by what it uses:

- CPU = 95th percentile of measured use × (1 + `cpuHeadroom`, default 25%);
- memory = peak measured use × (1 + `memoryHeadroom`, default 15%). Peak, not p95, because running out of memory kills a pod;
- per-pod suggestions are rounded up to 10m CPU and 16 MiB;
- workloads with fewer than 60 samples (one hour at the default interval) keep their current requests;
- only one load balancer is counted (one ingress controller in front of the services).

## Recommendations

| Recommendation | When |
|---|---|
| Lower requests | requests exceed the suggestion by at least 200m CPU or 512 MiB in total for the workload |
| Raise memory request | peak memory per pod is above the request (eviction risk) |
| Set requests | the workload has no CPU or memory request at all |
| Delete unused volumes | persistent volumes in `Released` or `Available` state |
| Share one load balancer | more than one `Service type: LoadBalancer` |
| Right-size the cluster | the right-sized scenario is cheaper than the current-requests scenario |

Savings are shown at UpCloud prices. For request changes, the price of a vCPU-month and a GiB-month is derived from the
cheapest plan with 4 GiB per vCPU, split evenly between CPU and memory.

## How accurate is it

- **Percentiles** are read from histogram buckets 5% wide and rounded up, so usage is overestimated by at most 5%.
- **Short spikes** between two samples are not seen. metrics-server itself averages over its own window (usually 15–30 s).
- **Coverage:** the report warns while history is under 24 hours. Weekly batch jobs and peaks need a full week.
- **Prices** are as of the date shown under "How this was calculated" in the report.
