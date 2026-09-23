# ADR-0005: Nx orchestrates every language; release-please owns versioning
**Decision:** Every build/lint/test/coverage/e2e is an Nx target (`nx affected` in CI and pre-push, cached). Nx Release is JS-centric, so semver + CHANGELOG come from release-please reading Conventional Commits on `main`; merging its release PR tags, publishes signed GHCR images (Sigstore provenance, Trivy-scanned) and binaries.
**Consequences:** One mental model locally and in CI; affected-only runs keep feedback fast.
