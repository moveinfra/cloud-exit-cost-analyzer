# Development

Requirements: Go (version in `go.mod`), Helm 3+, and for a local run a kubeconfig with read access to a cluster
that has metrics-server.

## Test

```sh
go test ./...                                   # collector against a fake API, estimator, histograms, report rendering
gofmt -l .                                      # must print nothing
go vet ./...
helm lint charts/cloud-exit-cost-analyzer
helm template t charts/cloud-exit-cost-analyzer # render the manifests
```

To look at the report with sample data: `REPORT_OUT=/tmp/report.html go test ./internal/web` and open the file.

## Run against a cluster from your laptop

The binary falls back to your current kubeconfig context when it is not running in a pod. Pin the context so you
never point it at the wrong cluster by accident:

```sh
kubectl config view --minify --flatten --context my-test-cluster > /tmp/kubeconfig
KUBECONFIG=/tmp/kubeconfig DATA_DIR=/tmp SAMPLE_INTERVAL=15s LISTEN_ADDR=127.0.0.1:8080 go run ./cmd/analyzer
open http://localhost:8080
```

It only reads. History goes to `/tmp/usage.json`.

## Build the image

```sh
docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=dev -t analyzer:dev .
```

The Dockerfile cross-compiles on the build machine (no emulation) and runs on `distroless/static:nonroot` as UID 65532.

## Where to change things

| Change | Files |
|---|---|
| A new current cloud (e.g. GCP prices) | `tools/pricegen/<cloud>.go`, `internal/pricing/pricing.go` (`Load`), `internal/estimate/estimate.go` (`current`) |
| A new target cloud | `internal/pricing` (a type like `UpCloud`), `internal/estimate/estimate.go` (`scenarios`), the report template |
| Report layout or wording | `internal/web/report.html` |
| A new recommendation | `internal/estimate/estimate.go` (`recommendations`) and a case in `estimate_test.go` |
| A new Helm value | `values.yaml`, `templates/deployment.yaml` (env), `cmd/analyzer/main.go`, `docs/configuration.md` |

Keep the analyzer read-only and offline: no new RBAC write verbs, no runtime network calls outside the cluster.
