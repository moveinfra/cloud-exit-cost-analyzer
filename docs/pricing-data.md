# Price data

Prices live in `internal/pricing/data/` and are compiled into the binary with `go:embed`. The analyzer never calls a
price API at runtime.

| File | Source | Credentials | Contents |
|---|---|---|---|
| `aws.json` | [AWS Price List bulk API](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-ppslong.html) (`AmazonEC2`, `AmazonEKS`, `AWSELB` offers) | none | per region: Linux on-demand price per instance type, EBS price per GB-month by volume type, EKS cluster fee, NLB hourly fee |
| `azure.json` | [Azure Retail Prices API](https://learn.microsoft.com/en-us/rest/api/cost-management/retail-prices/azure-retail-prices) | none | per region: Linux pay-as-you-go price per VM size, AKS Standard tier fee |
| `upcloud.json` | [UpCloud API](https://developers.upcloud.com/1.3/4-pricing/) `GET /1.3/plan` and `GET /1.3/price` | `UPCLOUD_TOKEN` | per zone: every plan (name, cores, memory, storage, monthly EUR), MaxIOPS and Standard storage per GB-month, all Managed Kubernetes and Managed Load Balancer plans |
| `hetzner.json`, `ovhcloud.json`, `scaleway.json` | [EU Cloud Price Index](https://github.com/moveinfra/eu-cloud-prices) `data/eu-cloud-prices.json` (open data, CC BY 4.0), which reads each provider's own price API or price page monthly and records the source of every figure | none | one region each (Hetzner `fsn1`, OVHcloud Europe, Scaleway `fr-par-1`): x86 server plans with their family, block storage per GB-month, the smallest production load balancer, the production Kubernetes control plane |
| `fx.json` | [ECB euro reference rates](https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html) | none | units of each currency per 1 EUR |

Regions covered: AWS `eu-*`, `us-east-1`, `us-east-2`, `us-west-2`; Azure EU regions, `uksouth`, `eastus`, `eastus2`,
`westus2`. Add more in `tools/pricegen/aws.go` (`awsRegions`) and `tools/pricegen/azure.go` (`azureRegions`).

## Refreshing

```sh
go run ./tools/pricegen fx
go run ./tools/pricegen azure      # ~2 min
go run ./tools/pricegen aws        # ~5 GB download, streamed; ~5-10 min
UPCLOUD_TOKEN=... go run ./tools/pricegen upcloud
go run ./tools/pricegen eu         # Hetzner, OVHcloud, Scaleway; -from <file> reads a local copy of the index
go test ./...
```

The `Refresh prices` workflow does this on the 2nd of every month and opens a pull request. UpCloud is refreshed
only when the repository has an `UPCLOUD_TOKEN` secret (an UpCloud API token; read access is enough).

## Hetzner, OVHcloud and Scaleway specifics

- The three files come from the EU Cloud Price Index, not straight from the providers. The `source` field in each file
  is the provider's own API or page the index read; the index repository has the collectors and the hand-checked
  figures (block storage, load balancers, Kubernetes) with their sources.
- Only x86 plans with whole vCPUs are kept. Worker nodes use the families `general`, `compute` and `memory`
  (dedicated CPU). Shared-CPU plans are stored too, but only used for a self-managed control plane.
- Hetzner has no managed Kubernetes, so its file has no control plane price. The Hetzner plan list in the index is
  checked by hand when the index run has no Hetzner API token; the file's `note` says so.
- OVHcloud and Scaleway server prices are the hourly price × 730.

## UpCloud specifics

- The price API returns **euro cents per hour**. `pricegen upcloud` divides by 100 (`-unit`) and multiplies by
  **672 hours** (`-hours`) for servers, UpCloud's monthly cap. Checked against round plan prices (e.g. €3.00, €8.00).
- Storage, Managed Kubernetes and Managed Load Balancers have no such cap: UpCloud quotes them for a 30-day month, so
  they are multiplied by **720 hours** (`-service-hours`). Checked against https://upcloud.com/pricing/ on 2026-11-25:
  Production control plane €60, Production S load balancer €30.
- Estimates use the `prod-md` Managed Kubernetes plan (the Production control plane, up to 120 nodes) and the
  `production-small` Load Balancer plan. Change with `-uks-plan` and `-lb-plan`; all plans are stored in the JSON
  either way. `production-small` and `development` in `kubernetesPlans` are older plans the API still lists.
- Plan family is derived from the plan name prefix (`DEV-`, `STARTER-`, `HIMEM-`, …); plans without a prefix are General Purpose.

## Checking a refresh

Before merging a price pull request, spot-check a few well-known prices against the providers' pages, for example
`m5.large` in `eu-central-1` and `Standard_D4s_v5` in `westeurope`, and the plan table that `pricegen upcloud` prints.
