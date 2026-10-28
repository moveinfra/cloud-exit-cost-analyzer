## What changes for users

<!-- One or two sentences. -->

## Checks

- [ ] `go test ./...`, `go vet ./...`, `helm lint charts/cloud-exit-cost-analyzer` pass, `gofmt -l .` is empty
- [ ] New behaviour has a test
- [ ] New Helm values have a safe default and are documented in `docs/configuration.md`
- [ ] Price data changed only through `tools/pricegen` (command: …)
- [ ] Still read-only and offline
