# Forking

You are welcome to fork and run your own copy (Apache-2.0). These are the names tied to `moveinfra` and what to
change so your fork publishes under your own account.

| Where | What to change |
|---|---|
| `go.mod` | module path `github.com/moveinfra/cloud-exit-cost-analyzer` → yours, then update the imports (`go mod edit -module …` and a search-and-replace) |
| `.github/workflows/release.yml` | `IMAGE` and the `oci://ghcr.io/moveinfra/charts` push target |
| `charts/cloud-exit-cost-analyzer/values.yaml` | `image.repository` |
| `charts/cloud-exit-cost-analyzer/Chart.yaml` | `home`, `sources`, `maintainers`, `icon`, Artifact Hub annotations |
| `artifacthub-repo.yml` | delete it, or replace the `repositoryID` with your own from Artifact Hub |
| `internal/web/report.html` | the footer link and the "Get a quote" call to action (links to moveinfra.eu) |
| `README.md`, `docs/` | install commands and URLs |

Then:

1. Enable GitHub Actions on the fork.
2. Settings → Pages → source `gh-pages` (the branch appears after your first release).
3. Push a tag `v0.1.0`. Make the two packages public in your account's or organisation's package settings.
4. Optional: add an `UPCLOUD_TOKEN` secret so the monthly price refresh includes UpCloud.

## Adding another target cloud

The estimator compares UpCloud, Hetzner, OVHcloud and Scaleway. A provider is one JSON file in
`internal/pricing/data/` in the `pricing.Provider` shape (server plans with a family, block storage per GB-month, a load
balancer and a Kubernetes control plane price) plus its id in `pricing.ProviderIDs`; the estimator and the report pick
it up from there. Write the file with a generator in `tools/pricegen` so it can be refreshed. Pull requests that add a
provider upstream are welcome; see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Trademarks

AWS, Azure, Google Cloud, DigitalOcean, UpCloud, Hetzner, OVHcloud and Scaleway are trademarks of their owners. A fork must not present itself as
MoveInfra or as an official tool of any cloud provider.
