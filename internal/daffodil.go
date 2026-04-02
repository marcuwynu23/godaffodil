package internal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config configures a Daffodil deployment client.
type Config struct {
	RemoteUser string
	RemoteHost string
	RemotePath string
	Port       int
	SSHKeyPath string
	IgnoreFile string
	Verbose    bool
	Inventory  string
	Group      string
}

// Step defines one deployment step.
type Step struct {
	Name    string
	Command func() error
}

// Daffodil is a lightweight deployment helper for SSH/SCP workflows.
type Daffodil struct {
	remoteUser  string
	remoteHost  string
	remotePath  string
	port        int
	sshKeyPath  string
	ignoreFile  string
	verbose     bool
	excludeList []string
	targets     []InventoryTarget
}

type InventoryTarget struct {
	Name string
	Host string
	User string
	Port int
}

type WatchOptions struct {
	Paths      []string
	DebounceMS int
	RepoPath   string
	Branch     string
	Branches   []string
	Tags       bool
	TagPattern *regexp.Regexp
	Events     []string
	IntervalMS int
}

type Watcher struct {
	deployer   *Daffodil
	options    WatchOptions
	steps      []Step
	mu         sync.Mutex
	stopCh     chan struct{}
	stopped    bool
	deploying  bool
	pending    bool
	debounceAt time.Time
	lastFiles  map[string]time.Time
	lastGit    gitState
}

type gitState struct {
	branches map[string]string
	merges   map[string]string
	tags     []string
}

// New creates a new deployment client.
func New(cfg Config) (*Daffodil, error) {
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	if cfg.RemotePath == "" {
		cfg.RemotePath = "."
	}
	usingInventory := strings.TrimSpace(cfg.Inventory) != ""
	if !usingInventory {
		if strings.TrimSpace(cfg.RemoteUser) == "" {
			return nil, errors.New("remote user is required")
		}
		if strings.TrimSpace(cfg.RemoteHost) == "" {
			return nil, errors.New("remote host is required")
		}
	}

	d := &Daffodil{
		remoteUser: cfg.RemoteUser,
		remoteHost: cfg.RemoteHost,
		remotePath: cfg.RemotePath,
		port:       cfg.Port,
		sshKeyPath: cfg.SSHKeyPath,
		ignoreFile: cfg.IgnoreFile,
		verbose:    cfg.Verbose,
	}
	if strings.TrimSpace(d.ignoreFile) == "" {
		d.ignoreFile = ".scpignore"
	}
	d.excludeList = d.loadIgnoreList()
	if usingInventory {
		targets, err := loadInventoryTargets(cfg.Inventory, cfg.Group)
		if err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			return nil, errors.New("no hosts found in inventory")
		}
		d.targets = targets
	}

	return d, nil
}

type Options struct {
	Verbose bool
}

func (d *Daffodil) SetOption(opts Options) {
	d.verbose = opts.Verbose
}

// RunCommand executes a local shell command.
func (d *Daffodil) RunCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return errors.New("command must not be empty")
	}
	cmd := shellCommand(command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// Local is an alias for RunCommand.
func (d *Daffodil) Local(command string) error {
	return d.RunCommand(command)
}

// SSHCommand executes a command on the remote host over SSH.
func (d *Daffodil) SSHCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return errors.New("ssh command must not be empty")
	}
	args := []string{
		"-p", fmt.Sprintf("%d", d.port),
	}
	if d.sshKeyPath != "" {
		args = append(args, "-i", d.sshKeyPath)
	}
	args = append(args, d.remoteAddr(), command)

	cmd := exec.Command("ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// SSH is an alias for SSHCommand.
func (d *Daffodil) SSH(command string) error {
	return d.SSHCommand(command)
}

// MakeDirectory creates a directory inside the configured remote path.
func (d *Daffodil) MakeDirectory(dirName string) error {
	if strings.TrimSpace(dirName) == "" {
		return errors.New("directory name must not be empty")
	}
	target := remoteJoin(d.remotePath, dirName)
	return d.SSHCommand(fmt.Sprintf("mkdir -p %q", target))
}

// TransferFiles archives local content, uploads it via SCP, extracts remotely, and cleans up.
func (d *Daffodil) TransferFiles(localPath, destinationPath string) error {
	if strings.TrimSpace(localPath) == "" {
		return errors.New("local path must not be empty")
	}
	if destinationPath == "" {
		destinationPath = d.remotePath
	}

	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("stat local path: %w", err)
	}

	tmpArchive := filepath.Join(os.TempDir(), fmt.Sprintf("godaffodil-%d.tar.gz", time.Now().UnixNano()))
	defer func() {
		_ = os.Remove(tmpArchive)
	}()

	if err := createTarGz(tmpArchive, localPath, info.IsDir(), d.excludeList); err != nil {
		return fmt.Errorf("create archive: %w", err)
	}

	remoteArchive := remoteJoin(destinationPath, filepath.Base(tmpArchive))
	if err := d.scpFile(tmpArchive, remoteArchive); err != nil {
		return fmt.Errorf("scp upload failed: %w", err)
	}

	extractCmd := fmt.Sprintf(
		"mkdir -p %q && cd %q && tar -xzf %q && rm -f %q",
		destinationPath, destinationPath, remoteArchive, remoteArchive,
	)
	if err := d.SSHCommand(extractCmd); err != nil {
		return fmt.Errorf("remote extract failed: %w", err)
	}

	return nil
}

// Deploy executes steps sequentially and stops on first error.
func (d *Daffodil) Deploy(steps []Step) error {
	if len(steps) == 0 {
		return errors.New("deploy requires at least one step")
	}
	if len(d.targets) > 0 {
		for _, target := range d.targets {
			prevUser, prevHost, prevPort := d.remoteUser, d.remoteHost, d.port
			d.remoteUser, d.remoteHost = target.User, target.Host
			if target.Port > 0 {
				d.port = target.Port
			}
			if d.verbose {
				fmt.Printf("==== Deploying to [%s] (%s) ====\n", target.Name, target.Host)
			}
			err := d.deploySingle(steps)
			d.remoteUser, d.remoteHost, d.port = prevUser, prevHost, prevPort
			if err != nil {
				return err
			}
		}
		return nil
	}
	return d.deploySingle(steps)
}

func (d *Daffodil) deploySingle(steps []Step) error {
	for i, step := range steps {
		if step.Command == nil {
			return fmt.Errorf("step %d (%s) has no command", i+1, step.Name)
		}
		if d.verbose {
			fmt.Printf("Step %d: %s\n", i+1, step.Name)
		}
		if err := step.Command(); err != nil {
			return fmt.Errorf("deploy failed at step %d (%s): %w", i+1, step.Name, err)
		}
	}
	return nil
}

func (d *Daffodil) Watch(options WatchOptions) *Watcher {
	if options.DebounceMS <= 0 {
		options.DebounceMS = 2000
	}
	if options.IntervalMS <= 0 {
		options.IntervalMS = 5000
	}
	if len(options.Events) == 0 {
		options.Events = []string{"commit", "merge", "tag"}
	}
	if len(options.Branches) == 0 && options.Branch != "" {
		options.Branches = []string{options.Branch}
	}
	return &Watcher{
		deployer:  d,
		options:   options,
		stopCh:    make(chan struct{}),
		lastFiles: map[string]time.Time{},
		lastGit: gitState{
			branches: map[string]string{},
			merges:   map[string]string{},
		},
	}
}

func (w *Watcher) Deploy(steps []Step) error {
	if len(steps) == 0 {
		return errors.New("watch deploy requires steps")
	}
	if len(w.options.Paths) == 0 && strings.TrimSpace(w.options.RepoPath) == "" {
		return errors.New("watch requires at least one trigger source: paths or repo path")
	}
	w.steps = steps
	w.captureInitialState()
	go w.loop()
	return nil
}

func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	w.stopped = true
	close(w.stopCh)
}

func (w *Watcher) loop() {
	ticker := time.NewTicker(time.Duration(w.options.IntervalMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			changed := w.detectFileChange() || w.detectGitChange()
			if changed {
				w.scheduleDeploy()
			}
			w.runDeployIfNeeded()
		}
	}
}

func (w *Watcher) captureInitialState() {
	w.lastFiles = w.snapshotFiles()
	w.lastGit = w.readGitState()
}

func (w *Watcher) scheduleDeploy() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = true
	w.debounceAt = time.Now().Add(time.Duration(w.options.DebounceMS) * time.Millisecond)
}

func (w *Watcher) runDeployIfNeeded() {
	w.mu.Lock()
	if w.stopped || w.deploying || !w.pending || time.Now().Before(w.debounceAt) {
		w.mu.Unlock()
		return
	}
	w.pending = false
	w.deploying = true
	steps := w.steps
	w.mu.Unlock()

	if err := w.deployer.Deploy(steps); err != nil && w.deployer.verbose {
		fmt.Printf("watch deploy failed: %v\n", err)
	}

	w.mu.Lock()
	w.deploying = false
	w.mu.Unlock()
}

func (w *Watcher) detectFileChange() bool {
	if len(w.options.Paths) == 0 {
		return false
	}
	next := w.snapshotFiles()
	if len(next) != len(w.lastFiles) {
		w.lastFiles = next
		return true
	}
	for k, v := range next {
		if old, ok := w.lastFiles[k]; !ok || !old.Equal(v) {
			w.lastFiles = next
			return true
		}
	}
	return false
}

func (w *Watcher) snapshotFiles() map[string]time.Time {
	out := map[string]time.Time{}
	for _, p := range w.options.Paths {
		_ = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil {
				return nil
			}
			out[path] = info.ModTime()
			return nil
		})
	}
	return out
}

func (w *Watcher) detectGitChange() bool {
	if strings.TrimSpace(w.options.RepoPath) == "" {
		return false
	}
	next := w.readGitState()
	if gitChanged(w.lastGit, next) {
		w.lastGit = next
		return true
	}
	return false
}

func (w *Watcher) readGitState() gitState {
	state := gitState{
		branches: map[string]string{},
		merges:   map[string]string{},
	}
	events := map[string]bool{}
	for _, e := range w.options.Events {
		events[strings.ToLower(strings.TrimSpace(e))] = true
	}
	if (events["commit"] || events["merge"]) && len(w.options.Branches) > 0 {
		for _, br := range w.options.Branches {
			if h := w.gitCmd("rev-parse " + br); h != "" {
				state.branches[br] = h
			}
		}
	}
	if events["merge"] && len(w.options.Branches) > 0 {
		for _, br := range w.options.Branches {
			state.merges[br] = w.gitCmd("log --merges -1 --format=%H " + br)
		}
	}
	if w.options.Tags || events["tag"] {
		raw := w.gitCmd("tag --list")
		if raw != "" {
			for _, line := range strings.Split(raw, "\n") {
				tag := strings.TrimSpace(line)
				if tag == "" {
					continue
				}
				if w.options.TagPattern != nil && !w.options.TagPattern.MatchString(tag) {
					continue
				}
				state.tags = append(state.tags, tag)
			}
			sort.Strings(state.tags)
		}
	}
	return state
}

func (w *Watcher) gitCmd(args string) string {
	cmd := shellCommand("git " + args)
	cmd.Dir = w.options.RepoPath
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func gitChanged(prev, next gitState) bool {
	if len(prev.branches) != len(next.branches) || len(prev.merges) != len(next.merges) || len(prev.tags) != len(next.tags) {
		return true
	}
	for k, v := range next.branches {
		if prev.branches[k] != v {
			return true
		}
	}
	for k, v := range next.merges {
		if prev.merges[k] != v {
			return true
		}
	}
	for i := range next.tags {
		if prev.tags[i] != next.tags[i] {
			return true
		}
	}
	return false
}

func (d *Daffodil) remoteAddr() string {
	return fmt.Sprintf("%s@%s", d.remoteUser, d.remoteHost)
}

func (d *Daffodil) scpFile(localPath, remotePath string) error {
	args := []string{
		"-P", fmt.Sprintf("%d", d.port),
	}
	if d.sshKeyPath != "" {
		args = append(args, "-i", d.sshKeyPath)
	}
	args = append(args, localPath, fmt.Sprintf("%s:%s", d.remoteAddr(), remotePath))
	cmd := exec.Command("scp", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func shellCommand(command string) *exec.Cmd {
	// Keep cross-platform shell behavior simple for local commands.
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-c", command)
}

func createTarGz(targetArchive, sourcePath string, sourceIsDir bool, excludeList []string) error {
	outFile, err := os.Create(targetArchive)
	if err != nil {
		return err
	}
	defer outFile.Close()

	gzw := gzip.NewWriter(outFile)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	base := filepath.Clean(sourcePath)

	addFile := func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if sourceIsDir && path == base {
			return nil
		}
		if shouldExclude(path, base, excludeList) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		var rel string
		if sourceIsDir {
			rel, err = filepath.Rel(base, path)
			if err != nil {
				return err
			}
		} else {
			rel, err = filepath.Rel(filepath.Dir(base), path)
			if err != nil {
				return err
			}
		}
		header.Name = filepath.ToSlash(rel)

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	}

	if !sourceIsDir {
		info, err := os.Stat(base)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.Base(base)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		f, err := os.Open(base)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	}

	return filepath.WalkDir(base, addFile)
}

func (d *Daffodil) loadIgnoreList() []string {
	if _, err := os.Stat(d.ignoreFile); err != nil {
		_ = os.WriteFile(d.ignoreFile, []byte("# Add ignore patterns\n"), 0o644)
	}
	content, err := os.ReadFile(d.ignoreFile)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(content), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		v := strings.TrimSpace(line)
		if v == "" || strings.HasPrefix(v, "#") {
			continue
		}
		out = append(out, v)
	}
	return out
}

func remoteJoin(base, child string) string {
	if strings.HasSuffix(base, "/") {
		return base + strings.TrimPrefix(child, "/")
	}
	return base + "/" + strings.TrimPrefix(child, "/")
}

func shouldExclude(path, base string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	name := filepath.Base(path)
	for _, p := range patterns {
		pat := filepath.ToSlash(strings.TrimSpace(p))
		if pat == "" {
			continue
		}
		if name == pat || rel == pat || strings.HasSuffix(rel, pat) || strings.Contains(rel, pat) {
			return true
		}
		if strings.Contains(pat, "*") {
			if ok, _ := filepath.Match(pat, rel); ok {
				return true
			}
			if ok, _ := filepath.Match(pat, name); ok {
				return true
			}
		}
	}
	return false
}

func loadInventoryTargets(path, group string) ([]InventoryTarget, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	lines := strings.Split(string(content), "\n")
	currentGroup := ""
	var targets []InventoryTarget
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentGroup = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		if currentGroup == "" {
			continue
		}
		if group != "" && currentGroup != group {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		t := InventoryTarget{Name: parts[0], Port: 22}
		for _, p := range parts[1:] {
			kv := strings.SplitN(p, "=", 2)
			if len(kv) != 2 {
				continue
			}
			k := strings.TrimSpace(kv[0])
			v := strings.TrimSpace(kv[1])
			switch k {
			case "host":
				t.Host = v
			case "user":
				t.User = v
			case "port":
				if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 {
					t.Port = n
				}
			}
		}
		if t.Host != "" && t.User != "" {
			targets = append(targets, t)
		}
	}
	return targets, nil
}
