# Contributing

Thanks for helping. Bug reports, price corrections, new clouds and docs fixes are all welcome.

## Before you start

- **Small fix** (typo, docs, a clear bug): open a pull request directly.
- **Anything bigger** (a new cloud, a new scenario, a Helm value): open an issue first so we can agree on the approach.
- **Security issue**: do not open a public issue; see [SECURITY.md](SECURITY.md).

## Setup

```sh
git clone https://github.com/moveinfra/cloud-exit-cost-analyzer
cd cloud-exit-cost-analyzer
go test ./...
```

More in [docs/development.md](docs/development.md).

## Pull requests

- One topic per pull request, with a short description of what changes for users.
- `go test ./...`, `go vet ./...` and `helm lint charts/cloud-exit-cost-analyzer` pass; `gofmt -l .` prints nothing. CI checks all of this.
- New behaviour has a test. Estimator changes get a case in `internal/estimate/estimate_test.go` with the expected numbers.
- New Helm values have a default that keeps today's behaviour and a row in [docs/configuration.md](docs/configuration.md).
- Price data is only changed by `tools/pricegen`, never by hand. Say which command you ran.

## Ground rules for the tool

These keep it safe to install in anyone's cluster:

1. **Read-only.** No write verbs in the ClusterRole, no reading secrets or config maps.
2. **Offline.** No network calls at runtime except to the Kubernetes API. Prices are embedded at build time.
3. **Honest numbers.** If something cannot be priced, list it under "Not included" instead of guessing.

## Commit messages

Short imperative subject (`Add GCP price generator`), a body when the reason is not obvious.

## License

By contributing you agree that your contribution is licensed under the [Apache License 2.0](LICENSE).
