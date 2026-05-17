## Description

<!-- Briefly describe what this PR changes and why. -->

## Related Issue

<!-- Closes #123 or related to #456 -->

## Type of Change

- [ ] `feat` — New feature
- [ ] `fix` — Bug fix
- [ ] `docs` — Documentation only
- [ ] `perf` — Performance improvement
- [ ] `refactor` — Code change that neither fixes a bug nor adds a feature
- [ ] `chore` — Maintenance, dependencies, CI
- [ ] `test` — Adding or updating tests

## Breaking Changes

- [ ] Yes — **BREAKING** (describe below)
- [ ] No

<!-- If yes, explain the change and migration path. -->

## Testing Done

<!-- How did you test this? What commands did you run? -->

## Checklist

- [ ] Code is formatted (`gofmt -l .` shows no changes)
- [ ] `go vet ./...` passes
- [ ] `go test -race -short ./...` passes in Pure Go mode
- [ ] `make test-rust` passes (if changes affect `native/compute/`)
- [ ] CHANGELOG.md updated (if user-facing change)
- [ ] Documentation updated (if API or behavior change)
- [ ] `make clean` run before final commit — no generated files included
- [ ] `go mod tidy` reports clean `go.mod` / `go.sum`
