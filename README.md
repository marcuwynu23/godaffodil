# GoDaffodil

GoDaffodil is a Go version of `jsdaffodil` and `pydaffodil`.

It supports:
- Module/library usage in Go projects
- Executable CLI usage (`godaffodil`)
- Local command execution
- Remote SSH command execution
- Remote directory creation
- Archive-based file transfer (`tar.gz` + `scp` + remote extract)
- Watch-based deployment triggers (`watch()`)
- Multi-host deployment via `inventory.ini`
- Ignore patterns from `.scpignore` (or custom ignore file)

## Install

### As a module

```bash
go get github.com/marcuwynu23/godaffodil
```

### As an executable

```bash
go install github.com/marcuwynu23/godaffodil/cmd/godaffodil@latest
```

## Module Usage

```go
package main

import (
	"log"

	"github.com/marcuwynu23/godaffodil"
)

func main() {
	cli, err := godaffodil.New(godaffodil.Config{
		RemoteUser: "deploy",
		RemoteHost: "example.com",
		RemotePath: "/var/www/myapp",
		Port:       22,
		SSHKeyPath: "",
		IgnoreFile: ".scpignore",
		Verbose:    true,
	})
	if err != nil {
		log.Fatal(err)
	}

	steps := []godaffodil.Step{
		{Name: "Build", Command: func() error { return cli.RunCommand("npm run build") }},
		{Name: "Transfer", Command: func() error { return cli.TransferFiles("dist", "") }},
		{Name: "Restart", Command: func() error { return cli.SSHCommand("sudo systemctl restart myapp") }},
	}

	if err := cli.Deploy(steps); err != nil {
		log.Fatal(err)
	}
}
```

### Watch Example (files + git)

```go
watcher := cli.Watch(godaffodil.WatchOptions{
	Paths:      []string{"./dist", "./src"},
	DebounceMS: 2000,
	RepoPath:   ".",
	Branch:     "main",
	Tags:       true,
	Events:     []string{"commit", "merge", "tag"},
	IntervalMS: 5000,
})

if err := watcher.Deploy(steps); err != nil {
	log.Fatal(err)
}
// keep process running while watcher is active
select {}
```

### Multi-Host Inventory Example

```ini
[webservers]
server1 host=10.0.0.11 user=deploy port=22
server2 host=10.0.0.12 user=deploy port=22
```

```go
multi, err := godaffodil.New(godaffodil.Config{
	Inventory: "./inventory.ini",
	Group:     "webservers",
	RemotePath: "/var/www/myapp",
})
if err != nil {
	log.Fatal(err)
}
if err := multi.Deploy(steps); err != nil {
	log.Fatal(err)
}
```

Sample files:
- `samples/watch/main.go`
- `samples/inventory/main.go`

## CLI Usage

```bash
# Local command
godaffodil local "npm run build"

# Remote SSH command
godaffodil ssh --user deploy --host example.com --port 22 "uname -a"

# Create directory under remote path
godaffodil mkdir --user deploy --host example.com --remote-path /var/www/myapp "releases"

# Transfer local folder to remote destination
godaffodil transfer --user deploy --host example.com --remote-path /var/www/myapp --ignore-file .scpignore --dest /var/www/myapp/current dist

# Watch mode (single host)
godaffodil watch --user deploy --host example.com --paths ./dist,./src --repo-path . --branch main --events commit,merge,tag --tags=true --step-ssh "pm2 restart myapp"

# Watch mode (inventory.ini multi-host)
godaffodil watch --inventory ./inventory.ini --group webservers --paths ./dist --step-ssh "pm2 restart myapp"
```

## Notes

- Requires `ssh`, `scp`, and `tar` available in your environment and on the remote host.
- The `transfer` command creates a temporary archive and removes it after completion.
