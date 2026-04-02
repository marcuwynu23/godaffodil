package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/marcuwynu23/godaffodil"
	"gopkg.in/yaml.v3"
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
	case "run":
		if err := runFromYAML(os.Args[2:]); err != nil {
			exitErr(err)
		}
	default:
		printUsage()
		os.Exit(1)
	}
}

type yamlConfig struct {
	RemoteUser string `yaml:"remoteUser"`
	RemoteHost string `yaml:"remoteHost"`
	RemotePath string `yaml:"remotePath"`
	Port       int    `yaml:"port"`
	SSHKeyPath string `yaml:"sshKeyPath"`
	IgnoreFile string `yaml:"ignoreFile"`
	Verbose    bool   `yaml:"verbose"`
	InventoryFile  string        `yaml:"inventoryFile"`
	InventoryYml   string        `yaml:"inventoryYml"`
	InventoryGroup string        `yaml:"inventoryGroup"`
	Hosts          []yamlHost    `yaml:"hosts"`
	Steps []struct {
		Name            string `yaml:"name"`
		Type            string `yaml:"type"`
		Command         string `yaml:"command"`
		LocalPath       string `yaml:"localPath"`
		DestinationPath string `yaml:"destinationPath"`
	} `yaml:"steps"`
	Watch struct {
		Paths      []string `yaml:"paths"`
		RepoPath   string   `yaml:"repoPath"`
		Branch     string   `yaml:"branch"`
		Branches   []string `yaml:"branches"`
		Events     []string `yaml:"events"`
		Tags       bool     `yaml:"tags"`
		TagPattern string   `yaml:"tagPattern"`
		IntervalMS int      `yaml:"interval"`
		DebounceMS int      `yaml:"debounce"`
	} `yaml:"watch"`
}

type yamlHost struct {
	Name       string `yaml:"name"`
	Host       string `yaml:"host"`
	User       string `yaml:"user"`
	Port       int    `yaml:"port"`
	RemotePath string `yaml:"remotePath"`
}

type inventoryYAML struct {
	Hosts  []yamlHost            `yaml:"hosts"`
	Groups map[string][]yamlHost `yaml:"groups"`
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

func runFromYAML(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", ".daffodil.yml", "path to deployment YAML")
	watchMode := fs.Bool("watch", false, "run watch mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*configPath) == "" {
		return errors.New("run requires --config .daffodil.yml")
	}
	if filepath.Base(*configPath) != ".daffodil.yml" {
		return errors.New("config filename must be exactly '.daffodil.yml' (use samples/.daffodil.yml)")
	}

	raw, err := os.ReadFile(*configPath)
	if err != nil {
		return err
	}
	var cfg yamlConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if len(cfg.Steps) == 0 {
		return errors.New("config must define non-empty steps")
	}

	hosts := cfg.Hosts
	if len(hosts) == 0 {
		inventoryFile := pick(cfg.InventoryFile, cfg.InventoryYml)
		if inventoryFile != "" {
			invPath := inventoryFile
			if !filepath.IsAbs(invPath) {
				invPath = filepath.Join(filepath.Dir(*configPath), invPath)
			}
			invRaw, readErr := os.ReadFile(invPath)
			if readErr != nil {
				return readErr
			}
			var inv inventoryYAML
			if unmarshalErr := yaml.Unmarshal(invRaw, &inv); unmarshalErr != nil {
				return unmarshalErr
			}
			if cfg.InventoryGroup != "" && len(inv.Groups[cfg.InventoryGroup]) > 0 {
				hosts = inv.Groups[cfg.InventoryGroup]
			} else if len(inv.Hosts) > 0 {
				hosts = inv.Hosts
			}
		}
	}
	if len(hosts) == 0 && cfg.RemoteHost != "" && cfg.RemoteUser != "" {
		hosts = append(hosts, yamlHost{
			Name: "default", Host: cfg.RemoteHost, User: cfg.RemoteUser, Port: cfg.Port, RemotePath: cfg.RemotePath,
		})
	}
	if len(hosts) == 0 {
		return errors.New("config must define hosts[] or remoteUser/remoteHost")
	}

	for _, h := range hosts {
		d, err := godaffodil.New(godaffodil.Config{
			RemoteUser: pick(h.User, cfg.RemoteUser),
			RemoteHost: pick(h.Host, cfg.RemoteHost),
			RemotePath: pick(h.RemotePath, cfg.RemotePath),
			Port:       pickInt(h.Port, cfg.Port, 22),
			SSHKeyPath: cfg.SSHKeyPath,
			IgnoreFile: pick(cfg.IgnoreFile, ".scpignore"),
			Verbose:    cfg.Verbose,
		})
		if err != nil {
			return err
		}

		steps := make([]godaffodil.Step, 0, len(cfg.Steps))
		for _, s := range cfg.Steps {
			step := s
			name := step.Name
			if name == "" {
				name = step.Type
			}
			switch strings.ToLower(strings.TrimSpace(step.Type)) {
			case "local":
				steps = append(steps, godaffodil.Step{Name: name, Command: func() error { return d.Local(step.Command) }})
			case "ssh":
				steps = append(steps, godaffodil.Step{Name: name, Command: func() error { return d.SSHCommand(step.Command) }})
			case "transfer":
				steps = append(steps, godaffodil.Step{Name: name, Command: func() error { return d.TransferFiles(step.LocalPath, step.DestinationPath) }})
			default:
				return fmt.Errorf("unsupported step type: %s", step.Type)
			}
		}

		if *watchMode {
			var re *regexp.Regexp
			if strings.TrimSpace(cfg.Watch.TagPattern) != "" {
				compiled, compileErr := regexp.Compile(cfg.Watch.TagPattern)
				if compileErr != nil {
					return compileErr
				}
				re = compiled
			}
			err = d.Watch(godaffodil.WatchOptions{
				Paths:      cfg.Watch.Paths,
				RepoPath:   cfg.Watch.RepoPath,
				Branch:     cfg.Watch.Branch,
				Branches:   cfg.Watch.Branches,
				Events:     cfg.Watch.Events,
				Tags:       cfg.Watch.Tags,
				TagPattern: re,
				IntervalMS: pickInt(cfg.Watch.IntervalMS, 5000),
				DebounceMS: pickInt(cfg.Watch.DebounceMS, 2000),
			}).Deploy(steps)
			if err != nil {
				return err
			}
		} else {
			if err := d.Deploy(steps); err != nil {
				return err
			}
		}
	}
	if *watchMode {
		fmt.Println("watch active; press Ctrl+C to exit")
		select {}
	}
	return nil
}

func pick(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func pickInt(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
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
	fmt.Println("  godaffodil run --config samples/.daffodil.yml [--watch]")
}
