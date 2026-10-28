# Security policy

## Reporting a vulnerability

Please report security issues privately, not in public issues:

- GitHub: **Security → Report a vulnerability** on this repository, or
- email **hello@moveinfra.eu** with "security" in the subject.

Include the version, how to reproduce, and the impact you see. We reply within 3 working days and keep you
updated until a fix is released. We credit reporters in the release notes unless you ask us not to.

## Supported versions

Fixes go into the latest release. Upgrade with `helm upgrade` to get them.

## What the analyzer can and cannot do

Useful when you assess its risk in your cluster:

- **Permissions:** cluster-wide `get` and `list` on nodes, pods, services, persistent volumes, storage classes and
  `metrics.k8s.io`. No write verbs. No access to secrets or config maps.
- **Network:** it calls only the Kubernetes API. Prices are compiled into the binary; there is no telemetry.
- **Container:** distroless image, non-root (UID 65532), read-only root filesystem, all capabilities dropped,
  `RuntimeDefault` seccomp profile.
- **Data at rest:** a usage history file (`usage.json`) on its own PVC, containing workload names and CPU/memory
  numbers. No pod specs, environment variables or logs.
- **Report:** served on a `ClusterIP` service without authentication. It shows workload names and sizes; reach it
  with `kubectl port-forward`, or put authentication in front of any ingress you add.
