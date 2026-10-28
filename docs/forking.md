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

The estimator is built around UpCloud today. To compare against Hetzner, Scaleway or OVHcloud, add a generator in
`tools/pricegen`, a price type in `internal/pricing`, and a target option in `internal/estimate`. Pull requests that do
this upstream are welcome; see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Trademarks

AWS, Azure, Google Cloud, DigitalOcean and UpCloud are trademarks of their owners. A fork must not present itself as
MoveInfra or as an official tool of any cloud provider.
