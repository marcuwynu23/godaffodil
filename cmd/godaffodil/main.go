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
	if len(os.Args) < 2 || os.Args[1] != "run" {
		printUsage()
		os.Exit(1)
	}
	if err := runFromYAML(os.Args[2:]); err != nil {
		exitErr(err)
	}
}

type yamlConfig struct {
	RemoteUser     string `yaml:"remoteUser"`
	RemoteHost     string `yaml:"remoteHost"`
	RemotePath     string `yaml:"remotePath"`
	Port           int    `yaml:"port"`
	SSHKeyPath     string `yaml:"sshKeyPath"`
	IgnoreFile     string `yaml:"ignoreFile"`
	Verbose        bool   `yaml:"verbose"`
	InventoryFile  string `yaml:"inventoryFile"`
	InventoryYml   string `yaml:"inventoryYml"`
	InventoryGroup string `yaml:"inventoryGroup"`
	Hosts          []yamlHost `yaml:"hosts"`
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
			targets, loadErr := godaffodil.LoadInventoryTargets(invPath, cfg.InventoryGroup)
			if loadErr != nil {
				return loadErr
			}
			for _, t := range targets {
				hosts = append(hosts, yamlHost{
					Name: t.Name,
					Host: t.Host,
					User: t.User,
					Port: t.Port,
				})
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
	fmt.Println("godaffodil — YAML-config deployment runner")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  godaffodil run --config path/to/.daffodil.yml [--watch]")
	fmt.Println()
	fmt.Println("  --config   Path to .daffodil.yml (filename must be exactly .daffodil.yml)")
	fmt.Println("  --watch    Use watch section in the YAML and keep the process running")
	fmt.Println()
	fmt.Println("Example:")
	fmt.Println("  godaffodil run --config samples/.daffodil.yml")
	fmt.Println("  godaffodil run --config samples/.daffodil.yml --watch")
	fmt.Println()
	fmt.Println("For ad-hoc local/SSH/transfer/watch flows, use the godaffodil Go API in your own program (see samples/).")
}
