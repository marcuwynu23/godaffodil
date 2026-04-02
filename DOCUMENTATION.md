# GoDaffodil — Developer Documentation

Documentation for contributors and maintainers. Usage patterns are described in [GUIDELINES.md](./GUIDELINES.md) and [README.md](./README.md).

## Table of contents

1. [Project overview](#project-overview)
2. [Repository layout](#repository-layout)
3. [Architecture](#architecture)
4. [Packages](#packages)
5. [CLI (`cmd/godaffodil`)](#cli-cmdgodaffodil)
6. [Features aligned with the Daffodil family](#features-aligned-with-the-daffodil-family)
7. [Development setup](#development-setup)
8. [Testing](#testing)
9. [Distribution](#distribution)
10. [Extension points](#extension-points)
11. [Runtime requirements](#runtime-requirements)
12. [Code style](#code-style)
13. [Additional resources](#additional-resources)
14. [Questions](#questions)

## Project overview

GoDaffodil is the **Go** implementation of the Daffodil tools. It provides:

- A **Go module** (`github.com/marcuwynu23/godaffodil`) wrapping an **`internal`** package
- Archive-based transfer using **`tar`**, **`scp`**, and remote extraction (requires those tools on PATH; remote needs `tar` for extract)
- **`Watch`** for file + Git–driven deploy loops
- **`LoadInventoryTargets`** for Ansible-style **`inventory.ini`**
- A minimal **CLI**: **`godaffodil run --config .daffodil.yml [--watch]`** only

Sister projects: [JSDaffodil](https://github.com/marcuwynu23/jsdaffodil) (Node.js), [PyDaffodil](https://github.com/marcuwynu23/pydaffodil) (Python).

End-user CLI and API details belong in [GUIDELINES.md](./GUIDELINES.md) and [README.md](./README.md), not in this file.

## Repository layout

```
godaffodil/
├── LICENSE
├── CONTRIBUTING.md
├── DOCUMENTATION.md    # This file
├── GUIDELINES.md
├── README.md
├── go.mod
├── go.sum
├── godaffodil.go     # Public API re-exports + New + LoadInventoryTargets
├── internal/
│   └── daffodil.go   # Implementation: Daffodil, Watch, transfer, inventory parse
├── cmd/
│   └── godaffodil/
│       ├── main.go   # YAML-only CLI: run --config
│       └── main_test.go
└── samples/          # watch/, inventory/, .daffodil.yml
```

## Architecture

```
User program or CLI
        │
        ▼
┌───────────────────┐
│  godaffodil API   │  godaffodil.go → internal.Daffodil
│  (exported types) │
└─────────┬─────────┘
          │
          ├── exec: ssh, scp, tar (local + remote)
├── LoadInventoryTargets(path, group)
└── Watch → Deploy loop
```

- **`internal`** is not importable by external modules path-wise; consumers use **`github.com/marcuwynu23/godaffodil`** only.
- **Inventory**: `LoadInventoryTargets` parses INI sections and `host=` / `user=` / `port=` tokens per line (see implementation for exact grammar).

## Packages

| Path | Role |
| ---- | ---- |
| `godaffodil.go` | Type aliases (`Config`, `Daffodil`, `Step`, `WatchOptions`, `Watcher`, `InventoryTarget`), `New`, `LoadInventoryTargets` |
| `internal/daffodil.go` | Core logic, SSH/SCP/tar integration, watch loop, multi-host targeting |

## CLI (`cmd/godaffodil`)

- **Invocation**: `godaffodil run --config path/to/.daffodil.yml` and optional **`--watch`**
- **Only** subcommand: **`run`** (unlike **JSDaffodil** / **PyDaffodil**, which use `jsdaffodil --config` / `pydaffodil --config` without a `run` token)
- Flags: **`--config`** (basename must be exactly `.daffodil.yml`), **`--watch`**
- Parses YAML into structs matching JSDaffodil/PyDaffodil: `steps`, `hosts`, `watch`, `inventoryFile`, `inventoryGroup`, remote defaults
- Step types: `local`, `ssh`, `transfer` (maps to `Local`, `SSHCommand`, `TransferFiles`)
- Does **not** expose extra subcommands (use the Go API or `samples/`)

## Features aligned with the Daffodil family

| Feature | Go notes |
| ------- | -------- |
| `.daffodil.yml` | Same fields as siblings; `godaffodil run --config` |
| `inventory.ini` | `LoadInventoryTargets`; YAML merges into host list when `hosts` empty |
| Watch | `WatchOptions` uses `regexp.Regexp` for tag pattern; YAML supplies string compiled in `main.go` |
| CLI surface | Go is **`run` only**; Node/Python use `jsdaffodil` / `pydaffodil` without a `run` subcommand |

## Development setup

```bash
git clone https://github.com/marcuwynu23/godaffodil.git
cd godaffodil
go test ./...
```

Use a recent Go toolchain compatible with `go.mod`.

## Testing

```bash
go test ./...
go vet ./...
```

`cmd/godaffodil` tests cover YAML loading and error paths without requiring a live SSH server.

## Distribution

```bash
go install github.com/marcuwynu23/godaffodil/cmd/godaffodil@latest
```

Tag releases per semantic versioning; document breaking API changes in README or a changelog if you add `CHANGELOG.md`.

## Extension points

- New **YAML step types**: extend `runFromYAML` switch in `cmd/godaffodil/main.go` and add parallel behavior in JS/Python if the feature is cross-cutting.
- **Inventory**: extend `LoadInventoryTargets` / `InventoryTarget` carefully to stay compatible with Ansible-style INI consumed by all three projects.
- Keep **`internal`** encapsulation; export new surface only through `godaffodil.go`.

## Runtime requirements

- Client: `ssh`, `scp`, `tar` available
- Remote: `tar` for extraction after upload

## Code style

- Run **`gofmt`** on all changes
- Return wrapped errors with context (`fmt.Errorf` with `%w`)
- Avoid logging secrets

## Additional resources

- [GUIDELINES.md](./GUIDELINES.md) — User-facing guide
- [CONTRIBUTING.md](./CONTRIBUTING.md) — PR workflow
- [README.md](./README.md) — Sister projects and quick links

## Questions

Open an issue on GitHub or refer to [JSDaffodil](https://github.com/marcuwynu23/jsdaffodil) / [PyDaffodil](https://github.com/marcuwynu23/pydaffodil) developer docs for cross-language behavior.
