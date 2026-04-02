# Contributing to GoDaffodil

Thank you for your interest in GoDaffodil. This guide aligns with the [Daffodil](https://github.com/marcuwynu23) family (see [JSDaffodil](https://github.com/marcuwynu23/jsdaffodil) and [PyDaffodil](https://github.com/marcuwynu23/pydaffodil)).

## Documentation overview

| Document | Purpose |
| -------- | ------- |
| [GUIDELINES.md](./GUIDELINES.md) | Usage: Go API, `inventory.ini`, `godaffodil run`, `Watch()`, troubleshooting |
| [DOCUMENTATION.md](./DOCUMENTATION.md) | Developer guide: module layout, `internal/` package, tests, CLI |
| [README.md](./README.md) | Overview, install, sister projects |
| [LICENSE](./LICENSE) | MIT License |

## Getting started

1. Read [GUIDELINES.md](./GUIDELINES.md) for API and CLI expectations.
2. Read [DOCUMENTATION.md](./DOCUMENTATION.md) for repository layout and `go test` usage.
3. Ensure **Go** matches `go.mod` and that **`ssh`**, **`scp`**, and **`tar`** are available when testing transfer behavior.

## Development setup

```bash
git clone https://github.com/marcuwynu23/godaffodil.git
cd godaffodil
go test ./...
```

Format before committing:

```bash
gofmt -w .
go vet ./...
```

## Contribution workflow

1. **Fork** and branch: `feature/short-name` or `fix/short-name`.
2. **Implement** with tests where practical (`cmd/godaffodil` has YAML-focused tests; extend as needed).
3. **Run** `go test ./...` and fix any vet issues.
4. **Update docs** ([GUIDELINES.md](./GUIDELINES.md), [README.md](./README.md)) if behavior or CLI flags change.
5. **Open a Pull Request** with a clear description.

## Commit messages

Prefer [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` — New feature
- `fix:` — Bug fix
- `docs:` — Documentation
- `test:` — Tests
- `chore:` — CI, tooling

## CLI scope

The official binary intentionally supports **`godaffodil run --config .daffodil.yml`** only. Ad-hoc `ssh` / `transfer` / `watch` subcommands are out of scope for the CLI; use the **Go API** in application code or `samples/`. Do not reintroduce removed subcommands without a project-wide decision across JSDaffodil / PyDaffodil / GoDaffodil.

## Code review

- Keep changes focused; match existing Go style (`gofmt`, idiomatic error handling).
- Exported APIs live in `godaffodil.go` (re-exports from `internal/`); avoid breaking changes without a version bump plan.

## Reporting issues

Include:

- Go version (`go version`)
- OS
- Minimal reproduction (YAML snippet, `inventory.ini` snippet if relevant)
- Expected vs actual behavior

## Security

Report sensitive issues privately (e.g. GitHub Security Advisories). Do not commit credentials or keys.

## Questions

See [GUIDELINES.md](./GUIDELINES.md) and [DOCUMENTATION.md](./DOCUMENTATION.md); open an issue if needed.

Thank you for contributing to GoDaffodil.
