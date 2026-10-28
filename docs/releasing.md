# Releasing

Push a tag `vX.Y.Z` on `main`:

```sh
git tag -a v0.2.0 -m v0.2.0
git push origin v0.2.0
```

The `Release` workflow (`.github/workflows/release.yml`) then:

1. runs the tests;
2. builds the image for `linux/amd64` and `linux/arm64` and pushes `ghcr.io/moveinfra/cloud-exit-cost-analyzer:X.Y.Z` and `:latest`;
3. sets the chart `version` and `appVersion` to `X.Y.Z`, packages it, and pushes it to `oci://ghcr.io/moveinfra/charts`;
4. creates a GitHub release with the chart `.tgz` and generated notes;
5. merges the new version into `index.yaml` on the `gh-pages` branch (the Helm repository at
   `https://moveinfra.github.io/cloud-exit-cost-analyzer`) and copies `artifacthub-repo.yml` there.

Artifact Hub picks up the new version from `gh-pages` within about 30 minutes.

## Versioning

- Patch (`0.1.1`): fixes, price refreshes.
- Minor (`0.2.0`): new features, new Helm values (with defaults that keep the old behaviour).
- Major: a Helm value removed or renamed, or a change to what the chart deploys that needs action on upgrade.

## One-time setup (already done for moveinfra)

- Repository → Settings → Pages: source `gh-pages`, folder `/`.
- Organisation → Packages: allow public packages; set both packages (`cloud-exit-cost-analyzer`,
  `charts/cloud-exit-cost-analyzer`) to **Public** after their first push.
- Artifact Hub: add the Helm repository URL; its repository ID is in `artifacthub-repo.yml`.
