# GoDaffodil

GoDaffodil is a Go version of `jsdaffodil` and `pydaffodil`.

It supports:
- Module/library usage in Go projects
- Executable CLI usage (`godaffodil`)
- Local command execution
- Remote SSH command execution
- Remote directory creation
- Archive-based file transfer (`tar.gz` + `scp` + remote extract)

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

## CLI Usage

```bash
# Local command
godaffodil local "npm run build"

# Remote SSH command
godaffodil ssh --user deploy --host example.com --port 22 "uname -a"

# Create directory under remote path
godaffodil mkdir --user deploy --host example.com --remote-path /var/www/myapp "releases"

# Transfer local folder to remote destination
godaffodil transfer --user deploy --host example.com --remote-path /var/www/myapp --dest /var/www/myapp/current dist
```

## Notes

- Requires `ssh`, `scp`, and `tar` available in your environment and on the remote host.
- The `transfer` command creates a temporary archive and removes it after completion.
