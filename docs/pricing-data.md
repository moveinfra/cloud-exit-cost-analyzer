# Price data

Prices live in `internal/pricing/data/` and are compiled into the binary with `go:embed`. The analyzer never calls a
price API at runtime.

| File | Source | Credentials | Contents |
|---|---|---|---|
| `aws.json` | [AWS Price List bulk API](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-ppslong.html) (`AmazonEC2`, `AmazonEKS`, `AWSELB` offers) | none | per region: Linux on-demand price per instance type, EBS price per GB-month by volume type, EKS cluster fee, NLB hourly fee |
| `azure.json` | [Azure Retail Prices API](https://learn.microsoft.com/en-us/rest/api/cost-management/retail-prices/azure-retail-prices) | none | per region: Linux pay-as-you-go price per VM size, AKS Standard tier fee |
| `upcloud.json` | [UpCloud API](https://developers.upcloud.com/1.3/4-pricing/) `GET /1.3/plan` and `GET /1.3/price` | `UPCLOUD_TOKEN` | per zone: every plan (name, cores, memory, storage, monthly EUR), MaxIOPS and Standard storage per GB-month, all Managed Kubernetes and Managed Load Balancer plans |
| `fx.json` | [ECB euro reference rates](https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html) | none | units of each currency per 1 EUR |

Regions covered: AWS `eu-*`, `us-east-1`, `us-east-2`, `us-west-2`; Azure EU regions, `uksouth`, `eastus`, `eastus2`,
`westus2`. Add more in `tools/pricegen/aws.go` (`awsRegions`) and `tools/pricegen/azure.go` (`azureRegions`).

## Refreshing

```sh
go run ./tools/pricegen fx
go run ./tools/pricegen azure      # ~2 min
go run ./tools/pricegen aws        # ~5 GB download, streamed; ~5-10 min
UPCLOUD_TOKEN=... go run ./tools/pricegen upcloud
go test ./...
```

The `Refresh prices` workflow does this on the 2nd of every month and opens a pull request. UpCloud is refreshed
only when the repository has an `UPCLOUD_TOKEN` secret (an UpCloud API token; read access is enough).

## UpCloud specifics

- The price API returns **euro cents per hour**. `pricegen upcloud` divides by 100 (`-unit`) and multiplies by
  **672 hours** (`-hours`), UpCloud's monthly cap. Checked against round plan prices (e.g. €3.00, €8.00).
- Estimates use the `production-small` Managed Kubernetes and Load Balancer plans. Change with
  `-uks-plan` and `-lb-plan`; all plans are stored in the JSON either way.
- Plan family is derived from the plan name prefix (`DEV-`, `STARTER-`, `HIMEM-`, …); plans without a prefix are General Purpose.

## Checking a refresh

Before merging a price pull request, spot-check a few well-known prices against the providers' pages, for example
`m5.large` in `eu-central-1` and `Standard_D4s_v5` in `westeurope`, and the plan table that `pricegen upcloud` prints.
