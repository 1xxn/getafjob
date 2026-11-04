# Contributing

## Ground rules

- **Fictional data only.** Never commit or attach real names, e-mail addresses, CVs, application data, screenshots
  of real data or personal paths. The example person is Max Mustermann (`max.mustermann@example.com`).
- **English everywhere** in code, comments, commits, docs and issues. The UI ships English and German; every text
  shown to people comes from the message catalogs, backend texts reach the UI as a code plus parameters.
- **Guard rails are not negotiable** in a pull request: no stealth or fingerprint tricks, no CAPTCHA solving, the
  browser sandbox stays on, no submit without a registered attempt, no form value without provenance. Changing one
  needs an ADR and the maintainer's approval first.

## Setup

```bash
git clone https://github.com/1xxn/getafjob.git && cd getafjob
go tool task hooks       # pre-commit: gofmt, go mod tidy, go vet; commit-msg: Conventional Commits
go tool task dev:up      # full stack on https://localhost:8443
```

Go 1.27.3 and Docker are the only requirements; every other tool is pinned in `go.mod` (`go tool …`).
golangci-lint and goreleaser run in containers (`go tool task lint`, `go tool task release:snapshot`).

## Before a pull request

```bash
go tool task check          # fmt, tidy, vet, golangci-lint, tests with -race, govulncheck, generated code
go tool task test:browser   # if you touched the runner or browser code
```

- One focused change per pull request, branched from `main`, linked to its issue (`Closes #n`).
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat(runner): …`).
- Hot paths get a benchmark; the pull request shows `benchstat` against `main` (> 10 % slower needs a reason).
- A new form quirk first gets an anonymised fixture in `testdata/fixtures/site/` and a failing test, then the fix in
  the shared engine logic (not per ATS).
- Merges are rebase-only after all checks are green.

## Tests

| Kind | Command | Notes |
|---|---|---|
| unit | `go test -short ./...` | table tests, no Docker |
| integration | `go test ./...` | testcontainers (PostgreSQL 18) – needs Docker |
| browser | `go tool task test:browser` | real Chromium inside the runner image, sandbox on |
| fuzz | `.github/scripts/fuzz-all.sh 1m` | every parser of foreign data has a fuzz target |
| e2e | `go test -tags e2e ./...` | against the running Compose stack |
| network | `GETAFJOB_NETWORK=1 go test -tags network ./...` | reads real sites, never submits; opt-in |
