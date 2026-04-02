package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
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
	case "watch":
		if err := runWatch(os.Args[2:]); err != nil {
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
	ignoreFile := fs.String("ignore-file", ".scpignore", "scp ignore file path")
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
		IgnoreFile: *ignoreFile,
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
	ignoreFile := fs.String("ignore-file", ".scpignore", "scp ignore file path")
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
		IgnoreFile: *ignoreFile,
		Verbose:    *verbose,
	}, fs.Arg(0), nil
}

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	user := fs.String("user", "", "remote SSH user")
	host := fs.String("host", "", "remote SSH host")
	remotePath := fs.String("remote-path", ".", "remote path")
	ignoreFile := fs.String("ignore-file", ".scpignore", "scp ignore file path")
	inventory := fs.String("inventory", "", "inventory.ini path")
	group := fs.String("group", "", "inventory group")
	repoPath := fs.String("repo-path", "", "git repo path to watch")
	paths := fs.String("paths", "", "comma separated local paths")
	branch := fs.String("branch", "", "git branch")
	branches := fs.String("branches", "", "comma separated git branches")
	events := fs.String("events", "commit,merge,tag", "comma separated events")
	tags := fs.Bool("tags", true, "watch tags")
	tagPattern := fs.String("tag-pattern", "", "regexp for tag filtering")
	interval := fs.Int("interval", 5000, "watch interval ms")
	debounce := fs.Int("debounce", 2000, "debounce ms")
	stepSSH := fs.String("step-ssh", "", "single SSH command step to run on trigger")
	stepLocal := fs.String("step-local", "", "single local command step to run on trigger")
	verbose := fs.Bool("verbose", false, "verbose output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *stepSSH == "" && *stepLocal == "" {
		return errors.New("at least one of --step-ssh or --step-local is required")
	}
	d, err := godaffodil.New(godaffodil.Config{
		RemoteUser: *user,
		RemoteHost: *host,
		RemotePath: *remotePath,
		IgnoreFile: *ignoreFile,
		Inventory:  *inventory,
		Group:      *group,
		Verbose:    *verbose,
	})
	if err != nil {
		return err
	}
	var steps []godaffodil.Step
	if *stepLocal != "" {
		cmd := *stepLocal
		steps = append(steps, godaffodil.Step{Name: "Local step", Command: func() error { return d.Local(cmd) }})
	}
	if *stepSSH != "" {
		cmd := *stepSSH
		steps = append(steps, godaffodil.Step{Name: "SSH step", Command: func() error { return d.SSHCommand(cmd) }})
	}
	var watchedPaths []string
	for _, p := range strings.Split(*paths, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			watchedPaths = append(watchedPaths, p)
		}
	}
	var watchedBranches []string
	for _, b := range strings.Split(*branches, ",") {
		b = strings.TrimSpace(b)
		if b != "" {
			watchedBranches = append(watchedBranches, b)
		}
	}
	if len(watchedBranches) == 0 && strings.TrimSpace(*branch) != "" {
		watchedBranches = []string{strings.TrimSpace(*branch)}
	}
	var watchedEvents []string
	for _, e := range strings.Split(*events, ",") {
		e = strings.TrimSpace(e)
		if e != "" {
			watchedEvents = append(watchedEvents, e)
		}
	}
	var compiledTagPattern *regexp.Regexp
	if strings.TrimSpace(*tagPattern) != "" {
		r, compileErr := regexp.Compile(*tagPattern)
		if compileErr != nil {
			return fmt.Errorf("invalid --tag-pattern: %w", compileErr)
		}
		compiledTagPattern = r
	}
	if err := d.Watch(godaffodil.WatchOptions{
		Paths:      watchedPaths,
		RepoPath:   *repoPath,
		Branch:     *branch,
		Branches:   watchedBranches,
		IntervalMS: *interval,
		DebounceMS: *debounce,
		Tags:       *tags,
		TagPattern: compiledTagPattern,
		Events:     watchedEvents,
	}).Deploy(steps); err != nil {
		return err
	}
	fmt.Println("watch active; press Ctrl+C to exit")
	select {}
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
	fmt.Println("  godaffodil watch [--inventory ./inventory.ini --group webservers] [--paths ./dist,./src] [--repo-path .] [--branch main|--branches main,staging] [--events commit,merge,tag] [--tags=true] [--tag-pattern '^v\\\\d+\\\\.\\\\d+\\\\.\\\\d+$'] --step-ssh \"pm2 restart app\"")
}
