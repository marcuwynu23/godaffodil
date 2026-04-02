package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/marcuwynu23/godaffodil"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "local":
		if err := runLocal(os.Args[2:]); err != nil {
			exitErr(err)
		}
	case "ssh":
		if err := runSSH(os.Args[2:]); err != nil {
			exitErr(err)
		}
	case "mkdir":
		if err := runMkdir(os.Args[2:]); err != nil {
			exitErr(err)
		}
	case "transfer":
		if err := runTransfer(os.Args[2:]); err != nil {
			exitErr(err)
		}
	default:
		printUsage()
		os.Exit(1)
	}
}

func runLocal(args []string) error {
	if len(args) == 0 {
		return errors.New("local command is required")
	}
	d, err := godaffodil.New(godaffodil.Config{
		RemoteUser: "local",
		RemoteHost: "localhost",
	})
	if err != nil {
		return err
	}
	return d.RunCommand(strings.Join(args, " "))
}

func runSSH(args []string) error {
	fs := flag.NewFlagSet("ssh", flag.ContinueOnError)
	cfg, command, err := parseRemoteFlags(fs, args)
	if err != nil {
		return err
	}
	d, err := godaffodil.New(cfg)
	if err != nil {
		return err
	}
	return d.SSHCommand(command)
}

func runMkdir(args []string) error {
	fs := flag.NewFlagSet("mkdir", flag.ContinueOnError)
	cfg, command, err := parseRemoteFlags(fs, args)
	if err != nil {
		return err
	}
	d, err := godaffodil.New(cfg)
	if err != nil {
		return err
	}
	return d.MakeDirectory(command)
}

func runTransfer(args []string) error {
	fs := flag.NewFlagSet("transfer", flag.ContinueOnError)
	user := fs.String("user", "", "remote SSH user")
	host := fs.String("host", "", "remote SSH host")
	port := fs.Int("port", 22, "remote SSH port")
	remotePath := fs.String("remote-path", ".", "default remote path")
	key := fs.String("key", "", "ssh private key path")
	dest := fs.String("dest", "", "destination path (optional)")
	verbose := fs.Bool("verbose", false, "verbose output")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("transfer requires local path argument")
	}

	d, err := godaffodil.New(godaffodil.Config{
		RemoteUser: *user,
		RemoteHost: *host,
		RemotePath: *remotePath,
		Port:       *port,
		SSHKeyPath: *key,
		Verbose:    *verbose,
	})
	if err != nil {
		return err
	}
	return d.TransferFiles(fs.Arg(0), *dest)
}

func parseRemoteFlags(fs *flag.FlagSet, args []string) (godaffodil.Config, string, error) {
	user := fs.String("user", "", "remote SSH user")
	host := fs.String("host", "", "remote SSH host")
	port := fs.Int("port", 22, "remote SSH port")
	remotePath := fs.String("remote-path", ".", "default remote path")
	key := fs.String("key", "", "ssh private key path")
	verbose := fs.Bool("verbose", false, "verbose output")
	if err := fs.Parse(args); err != nil {
		return godaffodil.Config{}, "", err
	}
	if fs.NArg() < 1 {
		return godaffodil.Config{}, "", errors.New("command argument is required")
	}
	return godaffodil.Config{
		RemoteUser: *user,
		RemoteHost: *host,
		RemotePath: *remotePath,
		Port:       *port,
		SSHKeyPath: *key,
		Verbose:    *verbose,
	}, fs.Arg(0), nil
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func printUsage() {
	fmt.Println("godaffodil usage:")
	fmt.Println("  godaffodil local <shell command>")
	fmt.Println("  godaffodil ssh --user <u> --host <h> [--port 22] [--key ~/.ssh/id_rsa] \"<remote command>\"")
	fmt.Println("  godaffodil mkdir --user <u> --host <h> [--remote-path /var/www] \"<dir-name>\"")
	fmt.Println("  godaffodil transfer --user <u> --host <h> [--remote-path /var/www] [--dest /custom/path] <local-path>")
}
