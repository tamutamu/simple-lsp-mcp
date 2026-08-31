## What this changes

<!-- One or two sentences. Link the issue if there is one. -->

## Why

<!-- The problem this solves. -->

## Checklist

- [ ] `go test ./...` passes
- [ ] `go test -race ./...` passes
- [ ] `go vet ./...` passes
- [ ] `gofmt -l .` prints nothing
- [ ] Tests added or updated for behaviour changes
- [ ] `README.md` updated if user-visible behaviour changed
- [ ] `CHANGELOG.md` updated under `## [Unreleased]`

## Design constraints

This server is read-only and symbol-first. Confirm this change keeps that true:

- [ ] Does not write, create, or delete files
- [ ] Does not execute commands through a shell
- [ ] Does not add text search or a persistent index
- [ ] Does not rename or remove an existing tool (or is intentionally a breaking change, noted above)
