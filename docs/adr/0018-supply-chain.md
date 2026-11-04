# 0018. Supply chain: pinned tools, signed artifacts

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Decision outcome

- Go tools pinned with the `tool` directive in `go.mod` and run via `go tool`; golangci-lint and goreleaser pinned by
  version in CI and Taskfile.
- Actions pinned by SHA, `permissions: {}` by default, `persist-credentials: false`, harden-runner, zizmor.
- Images signed keyless with cosign, with SBOM and build provenance; release binaries with SBOM, signed checksums
  and provenance attestations.
- Renovate updates modules, actions, image digests and tool versions (grouped, 7-day cooldown).
- PR checks: govulncheck, Trivy, CodeQL, dependency review; OpenSSF Scorecard.

### Consequences

- Good, because every artifact is traceable to a commit and workflow.
- Bad, because updates arrive as regular Renovate pull requests to review.
