<div align="center">
  <h1>GoDaffodil</h1>
  <p><strong>Cross-Platform Deployment Automation Framework for Go</strong></p>
  <p>
    <img src="https://img.shields.io/github/go-mod/go-version/marcuwynu23/godaffodil?label=Go" alt="Go version"/>
    <img src="https://img.shields.io/github/stars/marcuwynu23/godaffodil.svg" alt="Stars"/>
    <img src="https://img.shields.io/github/license/marcuwynu23/godaffodil.svg" alt="License"/>
  </p>
</div>

---

## Overview

**GoDaffodil** is a lightweight deployment automation library and CLI for Go. It mirrors the design of [JSDaffodil](https://www.npmjs.com/package/@marcuwynu23/jsdaffodil) and [PyDaffodil](https://pypi.org/project/pydaffodil/): SSH remote commands, archive-based file transfer, optional **watch** triggers (files + Git), and **multi-host** runs via Ansible-style **`inventory.ini`**.

### Key Features

- **Library and CLI** — Use `godaffodil` as a Go module or invoke the `godaffodil` binary
- **Archive-Based File Transfer** — `tar.gz` packaging, `scp` transfer, remote extract
- **SSH Operations** — Remote command execution and directory creation
- **Ignore Patterns** — `.scpignore` (or custom path) for transfer exclusions
- **Watch-Based Deployments** — `Watch()` with file paths, Git repo, branches, tags, and events
- **Multi-Host Deployments** — `inventory.ini` groups for sequential deploys across hosts
- **YAML Runner** — `godaffodil run --config .daffodil.yml` for declarative steps

---

## Documentation and Examples

Sample programs live under **`samples/`**:

- `samples/watch/main.go` — Watch-driven deployment
- `samples/inventory/main.go` — Multi-host inventory deployment
- `samples/.daffodil.yml` — Reference YAML for `godaffodil run`

---

## Installation

### As a Go module

```bash
go get github.com/marcuwynu23/godaffodil
```

### As a CLI binary

```bash
go install github.com/marcuwynu23/godaffodil/cmd/godaffodil@latest
```

---

## Quick Start (library)

```go
package main

import (
	"log"

	"github.com/marcuwynu23/godaffodil"
)

func main() {
	d, err := godaffodil.New(godaffodil.Config{
		RemoteUser: "deployer",
		RemoteHost: "231.142.34.222",
		RemotePath: "/var/www/myapp",
		Port:       22,
		IgnoreFile: ".scpignore",
	})
	if err != nil {
		log.Fatal(err)
	}

	steps := []godaffodil.Step{
		{Name: "Transfer", Command: func() error { return d.TransferFiles("./dist", "/var/www/myapp") }},
		{Name: "Install", Command: func() error { return d.SSHCommand("cd /var/www/myapp && npm ci --omit=dev") }},
		{Name: "Restart", Command: func() error { return d.SSHCommand("pm2 restart myapp") }},
	}

	if err := d.Deploy(steps); err != nil {
		log.Fatal(err)
	}
}
```

---

## API Reference

### `godaffodil.New(cfg Config) (*Daffodil, error)`

```go
type Config struct {
	RemoteUser string // required when not using inventory
	RemoteHost string // required when not using inventory
	RemotePath string // default "."
	Port       int    // default 22
	SSHKeyPath string
	IgnoreFile string // default ".scpignore"
	Verbose    bool
	Inventory  string // path to inventory.ini (multi-host)
	Group      string // inventory group name when using Inventory
}
```

In **single-host** mode, `RemoteUser` and `RemoteHost` are required. With **`Inventory`** set, targets are loaded from `inventory.ini` and **`Group`** selects the section.

### Core methods

| Method | Description |
| ------ | ----------- |
| `RunCommand(cmd string) error` | Run a shell command locally |
| `SSHCommand(cmd string) error` | Run a command on the remote host |
| `MakeDirectory(name string) error` | Create a directory on the remote host |
| `TransferFiles(localPath, dest string) error` | Archive, transfer, extract (respects ignore file) |
| `Deploy(steps []Step) error` | Run steps in order; in inventory mode, once per host |
| `Watch(opts WatchOptions) *Watcher` | Returns a watcher; call `Deploy(steps)` on it |

### `WatchOptions` (high level)

- `Paths`, `DebounceMS`, `RepoPath`, `Branch`, `Branches`, `Tags`, `TagPattern` (`*regexp.Regexp`), `Events`, `IntervalMS`

### `godaffodil.LoadInventoryTargets(path, group string)`

Parses an Ansible-style `inventory.ini`. Exported for tools and tests; the `Daffodil` constructor uses the same parser when `Config.Inventory` is set.

---

## Advanced Topics

### Archive-Based Transfer

The library builds a temporary `tar.gz`, copies it with `scp`, extracts on the remote side, and cleans up. This minimizes SSH round-trips for large trees.

### Ignore file

Patterns in `.scpignore` (or `IgnoreFile`) exclude paths from packaged transfers.

### SSH and remote tools

The CLI and transfer path expect **`ssh`**, **`scp`**, and **`tar`** on the client **and** the remote host where extraction runs.

---

## Best Practices

- Verify `ssh user@host` works with keys before wiring automation.
- Keep secrets out of source control; use environment variables or your platform’s secret store.
- In watch mode, keep `DebounceMS` high enough to avoid deploy storms during rapid saves.
- For production, pin module versions in `go.mod` and test deploy steps in staging first.

---

## Configuration (struct summary)

| Field         | Notes |
| ------------- | ----- |
| `RemoteUser`  | SSH user (single-host) |
| `RemoteHost`  | Hostname or IP (single-host) |
| `RemotePath`  | Default remote working path |
| `Port`        | SSH port (default 22) |
| `SSHKeyPath`  | Optional explicit private key |
| `IgnoreFile`  | Ignore patterns file |
| `Verbose`     | Extra logging |
| `Inventory`   | Path to `inventory.ini` |
| `Group`       | Group name inside the inventory file |

---

## Watch-Based CI/CD

```go
w := d.Watch(godaffodil.WatchOptions{
	Paths:      []string{"./dist", "./src"},
	DebounceMS: 2000,
	RepoPath:   ".",
	Branches:   []string{"main", "staging"},
	Tags:       true,
	TagPattern: regexp.MustCompile(`^v\d+\.\d+\.\d+$`),
	Events:     []string{"commit", "merge", "tag"},
	IntervalMS: 5000,
})
if err := w.Deploy(steps); err != nil {
	log.Fatal(err)
}
// keep the process alive while the watcher runs
select {}
```

See `samples/watch/main.go` for a runnable example.

---

## Multi-Host Deployments with `inventory.ini`

```ini
[webservers]
server1 host=231.142.34.222 user=deployer port=22
server2 host=231.142.34.223 user=deployer
server3 host=231.142.34.224 user=ubuntu port=2200
```

```go
multi, err := godaffodil.New(godaffodil.Config{
	Inventory:  "./inventory.ini",
	Group:      "webservers",
	RemotePath: "/var/www/myapp",
})
if err != nil {
	log.Fatal(err)
}
if err := multi.Deploy(steps); err != nil {
	log.Fatal(err)
}
```

See `samples/inventory/main.go`.

---

## Requirements

- **Go** toolchain compatible with the module’s `go` directive
- **OpenSSH**-style `ssh` / `scp` available on `PATH`
- **`tar`** on the remote host for extraction

---

## CLI Usage

```bash
# Local command
godaffodil local "npm run build"

# Remote SSH command
godaffodil ssh --user deploy --host example.com --port 22 "uname -a"

# Create directory under remote path
godaffodil mkdir --user deploy --host example.com --remote-path /var/www/myapp "releases"

# Transfer directory to remote
godaffodil transfer --user deploy --host example.com --remote-path /var/www/myapp --ignore-file .scpignore --dest /var/www/myapp/current dist

# Watch (single host)
godaffodil watch --user deploy --host example.com --paths ./dist,./src --repo-path . --branch main --events commit,merge,tag --tags=true --step-ssh "pm2 restart myapp"

# Watch (inventory multi-host)
godaffodil watch --inventory ./inventory.ini --group webservers --paths ./dist --step-ssh "pm2 restart myapp"

# Declarative YAML
godaffodil run --config samples/.daffodil.yml
godaffodil run --config samples/.daffodil.yml --watch
```

### YAML host resolution (`godaffodil run`)

1. Inline **`hosts`** in `.daffodil.yml` (if present)
2. **`inventoryFile`** + **`inventoryGroup`** → `inventory.ini`
3. **`remoteUser`** + **`remoteHost`** for a single default host

Example inventory reference:

```yaml
inventoryFile: inventory.ini
inventoryGroup: webservers
```

---

## Contributing

Issues and pull requests are welcome. For larger changes, open an issue first to agree on scope and API impact.

---

## License

[MIT License](./LICENSE)

---

## Acknowledgments

Sister projects: [JSDaffodil](https://www.npmjs.com/package/@marcuwynu23/jsdaffodil) (Node.js), [PyDaffodil](https://pypi.org/project/pydaffodil/) (Python).

---

<div align="center">
  <p>Made with care by <a href="https://github.com/marcuwynu23">Mark Wayne B. Menorca</a></p>
</div>
