# GoDaffodil Usage Guidelines

End-user guide for the Go API and the **`godaffodil run`** YAML CLI: **`inventory.ini`**, **`.daffodil.yml`**, **`Watch()`**, and troubleshooting. For contributors and architecture, see [DOCUMENTATION.md](./DOCUMENTATION.md).

Sister projects: [JSDaffodil](https://github.com/marcuwynu23/jsdaffodil) (Node.js), [PyDaffodil](https://github.com/marcuwynu23/pydaffodil) (Python). Shared **`.daffodil.yml`** schema and inventory format.

## Table of contents

1. [Installation](#installation)
2. [Quick start](#quick-start)
3. [Configuration](#configuration)
4. [Core operations](#core-operations)
5. [Multi-host (`inventory.ini`)](#multi-host-inventoryini)
6. [Watch (`Watch`)](#watch-watch)
7. [YAML CLI](#yaml-cli)
8. [Ignore file (`.scpignore`)](#ignore-file-scpignore)
9. [Best practices](#best-practices)
10. [Troubleshooting](#troubleshooting)
11. [Additional resources](#additional-resources)

## Installation

**Module:**

```bash
go get github.com/marcuwynu23/godaffodil
```

**CLI binary:**

```bash
go install github.com/marcuwynu23/godaffodil/cmd/godaffodil@latest
```

Requires **`ssh`**, **`scp`**, and **`tar`** on the client; the remote host needs **`tar`** for extraction.

## Quick start

```go
package main

import (
	"log"

	"github.com/marcuwynu23/godaffodil"
)

func main() {
	d, err := godaffodil.New(godaffodil.Config{
		RemoteUser: "deploy",
		RemoteHost: "203.0.113.10",
		RemotePath: "/var/www/app",
		Port:       22,
		IgnoreFile: ".scpignore",
	})
	if err != nil {
		log.Fatal(err)
	}
	steps := []godaffodil.Step{
		{Name: "Upload", Command: func() error { return d.TransferFiles("./dist", "/var/www/app") }},
		{Name: "Reload", Command: func() error { return d.SSHCommand("systemctl reload nginx") }},
	}
	if err := d.Deploy(steps); err != nil {
		log.Fatal(err)
	}
}
```

## Configuration

### `godaffodil.Config` (single-host)

| Field | Purpose |
| ----- | ------- |
| `RemoteUser`, `RemoteHost` | Required when **not** using inventory |
| `RemotePath` | Default remote path (default `"."`) |
| `Port` | SSH port (default 22) |
| `SSHKeyPath` | Optional explicit private key |
| `IgnoreFile` | Ignore patterns (default `.scpignore`) |
| `Verbose` | Extra logging |

### Inventory (multi-host)

| Field | Purpose |
| ----- | ------- |
| `Inventory` | Path to `inventory.ini` |
| `Group` | Section name for hosts |

## Core operations

- **`RunCommand(cmd)`** — Local shell.
- **`SSHCommand(cmd)`** — Remote command.
- **`TransferFiles(local, dest)`** — Tar, scp, remote extract.
- **`MakeDirectory(name)`** — Remote mkdir under remote path context.
- **`Deploy(steps)`** — Sequential steps; inventory mode runs per host.

## Multi-host (`inventory.ini`)

```ini
[webservers]
app1 host=203.0.113.10 user=deploy port=22
app2 host=203.0.113.11 user=deploy
```

```go
d, err := godaffodil.New(godaffodil.Config{
	Inventory:  "./inventory.ini",
	Group:      "webservers",
	RemotePath: "/var/www/app",
})
```

See `samples/inventory/main.go`. You can also call **`godaffodil.LoadInventoryTargets(path, group)`** from tests or tools.

## Watch (`Watch`)

```go
import (
	"log"
	"regexp"

	"github.com/marcuwynu23/godaffodil"
)

w := d.Watch(godaffodil.WatchOptions{
	Paths:      []string{"./dist"},
	DebounceMS: 2000,
	RepoPath:   ".",
	Branches:   []string{"main"},
	Tags:       true,
	TagPattern: regexp.MustCompile(`^v\d+\.\d+\.\d+$`),
	Events:     []string{"commit", "merge", "tag"},
	IntervalMS: 5000,
})
if err := w.Deploy(steps); err != nil {
	log.Fatal(err)
}
select {} // keep process alive
```

See `samples/watch/main.go`.

## YAML CLI

The **only** supported CLI entrypoint:

```bash
godaffodil run --config samples/.daffodil.yml
godaffodil run --config samples/.daffodil.yml --watch
```

- **`--config`**: path whose **basename** is exactly **`.daffodil.yml`**.
- **`--watch`**: uses the `watch:` block in the file and blocks after starting watchers.

**Host resolution** (aligned with JSDaffodil / PyDaffodil):

1. **`hosts`** in YAML if present
2. **`inventoryFile`** + **`inventoryGroup`**
3. **`remoteUser`** + **`remoteHost`** for a single default host

**Node / Python note:** JSDaffodil and PyDaffodil use `jsdaffodil --config …` and `pydaffodil --config …` **without** a `run` subcommand.

There are **no** extra `godaffodil` subcommands for one-off SSH or transfer—use the Go API or `samples/`.

## Ignore file (`.scpignore`)

Patterns exclude paths from the transfer bundle. Set `IgnoreFile` in `Config` if not using the default filename.

## Best practices

- **Pin** the module version in production.
- Use **inventory** for multiple servers; keep **YAML** portable across the three official CLIs.
- **Debounce** watch options to avoid rapid repeat deploys.

## Troubleshooting

| Issue | Checks |
| ----- | ------ |
| `ssh`/`scp`/`tar` not found | Install OpenSSH and tar; ensure `PATH` |
| Remote extract fails | `tar` on remote; permissions on destination |
| `no hosts found` | YAML hosts, inventory path + group, or remote defaults |
| Watch exits immediately | `Watch().Deploy` returns; keep process alive with `select {}` in long-running apps |

## Additional resources

- [README.md](./README.md) — Overview and sister projects
- [DOCUMENTATION.md](./DOCUMENTATION.md) — Developer documentation (`internal/`, `cmd/godaffodil`)
- [CONTRIBUTING.md](./CONTRIBUTING.md) — How to contribute
- [samples/](./samples/) — Watch and inventory examples
