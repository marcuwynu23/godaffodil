package internal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
	Check      bool
	Inventory  string
	Group      string
}

// Step defines one deployment step.
type Step struct {
	Name         string
	When         string
	Command      func() error
	StateCommand func() (bool, error)
}

type StepResult struct {
	Name    string
	Status  string
	Changed bool
}

type DeployResult struct {
	Host    string
	Results []StepResult
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
	check       bool
	excludeList []string
	targets     []InventoryTarget
	sshRunFn    func(command string) (string, error)
	scpFn       func(localPath, remotePath string) error
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
		check:      cfg.Check,
	}
	if strings.TrimSpace(d.ignoreFile) == "" {
		d.ignoreFile = ".scpignore"
	}
	d.excludeList = d.loadIgnoreList()
	if usingInventory {
		targets, err := LoadInventoryTargets(cfg.Inventory, cfg.Group)
		if err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			return nil, errors.New("no hosts found in inventory")
		}
		d.targets = targets
	}

	d.check = cfg.Check
	d.sshRunFn = d.sshRun
	d.scpFn = d.scpFile

	return d, nil
}

type Options struct {
	Verbose bool
	Check   bool
}

func (d *Daffodil) SetOption(opts Options) {
	d.verbose = opts.Verbose
	d.check = opts.Check
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
	if _, err := d.sshRunFn(command); err != nil {
		return err
	}
	return nil
}

func (d *Daffodil) sshRun(command string) (string, error) {
	args := []string{
		"-p", fmt.Sprintf("%d", d.port),
	}
	if d.sshKeyPath != "" {
		args = append(args, "-i", d.sshKeyPath)
	}
	args = append(args, d.remoteAddr(), command)
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
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
	return d.SSHCommand(fmt.Sprintf("mkdir -p %s", shellQuote(target)))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (d *Daffodil) remoteResolvePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := d.remoteHome()
		if err != nil {
			return p
		}
		if p == "~" {
			return home
		}
		return home + "/" + strings.TrimPrefix(p, "~/")
	}
	if strings.HasPrefix(p, "/") {
		return p
	}
	return d.remotePath + "/" + strings.TrimPrefix(p, "/")
}

func (d *Daffodil) remoteHome() (string, error) {
	out, err := d.sshRunFn("printf '%s' \"$HOME\"")
	if err != nil {
		return "", err
	}
	home := strings.TrimSpace(out)
	if home == "" {
		return "", errors.New("unable to determine remote home directory")
	}
	return home, nil
}

func (d *Daffodil) readRemoteFile(path string) (string, bool, error) {
	quoted := shellQuote(path)
	cmd := fmt.Sprintf("if [ -f %s ]; then cat %s; else exit 3; fi", quoted, quoted)
	out, err := d.sshRunFn(cmd)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read remote file %s: %w", path, err)
	}
	return out, true, nil
}

func (d *Daffodil) writeRemoteFile(path string, content string) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if _, err := d.sshRunFn(fmt.Sprintf("mkdir -p %s", shellQuote(dir))); err != nil {
			return fmt.Errorf("failed to create remote directory %s: %w", dir, err)
		}
	}
	writeCmd := fmt.Sprintf("cat > %s <<'DAFFODIL_EOF'\n%s\nDAFFODIL_EOF", shellQuote(path), content)
	if _, err := d.sshRunFn(writeCmd); err != nil {
		return fmt.Errorf("failed to write remote file %s: %w", path, err)
	}
	return nil
}

func (d *Daffodil) remoteFileHash(path string) (string, error) {
	quoted := shellQuote(path)
	cmd := fmt.Sprintf(
		"if [ -f %s ]; then (sha256sum %s 2>/dev/null || shasum -a 256 %s 2>/dev/null || openssl dgst -sha256 %s 2>/dev/null) | awk '{print $NF}'; else exit 3; fi",
		quoted, quoted, quoted, quoted,
	)
	out, err := d.sshRunFn(cmd)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
			return "", nil
		}
		return "", fmt.Errorf("hash remote file %s: %w", path, err)
	}
	hash := strings.TrimSpace(out)
	if !isSHA256Hex(hash) {
		return "", nil
	}
	return hash, nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (d *Daffodil) remoteFileMatches(path string, content string) (bool, error) {
	expected := sha256String(content)
	actual, err := d.remoteFileHash(path)
	if err != nil {
		return false, err
	}
	if actual != "" {
		return actual == expected, nil
	}
	current, exists, err := d.readRemoteFile(path)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	return current == content, nil
}

func (d *Daffodil) remoteFileStat(path string) (map[string]string, error) {
	quoted := shellQuote(path)
	cmd := fmt.Sprintf(
		"if [ -f %s ]; then (stat -c '%%a %%U:%%G' %s 2>/dev/null); else exit 3; fi",
		quoted, quoted,
	)
	out, err := d.sshRunFn(cmd)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
			return nil, nil
		}
		return nil, fmt.Errorf("stat remote file %s: %w", path, err)
	}
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) < 1 {
		return map[string]string{}, nil
	}
	mode := parts[0]
	owner := ""
	if len(parts) > 1 {
		owner = parts[1]
	}
	return map[string]string{"mode": mode, "owner": owner}, nil
}

func sha256String(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// EnsureLineInFile appends `line` to a remote file only when it is absent.
// When a managed `marker` block is provided, the block content is replaced in place.
// Returns true when the host would change (or did change).
func (d *Daffodil) EnsureLineInFile(path string, line string, marker string) (bool, error) {
	if strings.TrimSpace(line) == "" {
		return false, errors.New("ensure line in file requires a line")
	}
	target := d.remoteResolvePath(path)
	current, _, err := d.readRemoteFile(target)
	if err != nil {
		return false, err
	}
	var next string
	if marker != "" {
		next = d.replaceManagedBlock(current, marker, line)
	} else {
		next = d.appendManagedLine(current, line)
	}
	if next == current {
		return false, nil
	}
	if d.check {
		return true, nil
	}
	if err := d.writeRemoteFile(target, next); err != nil {
		return false, err
	}
	return true, nil
}

func (d *Daffodil) appendManagedLine(current string, line string) string {
	lines := strings.Split(current, "\n")
	for _, existing := range lines {
		if strings.TrimRight(existing, "\r") == line {
			return current
		}
	}
	if current == "" {
		return line + "\n"
	}
	if strings.HasSuffix(current, "\n") {
		return current + line + "\n"
	}
	return current + "\n" + line + "\n"
}

func (d *Daffodil) replaceManagedBlock(current string, marker string, line string) string {
	marker = strings.TrimSpace(marker)
	begin := marker
	end := marker
	if strings.Contains(marker, "{mark}") {
		begin = strings.Replace(marker, "{mark}", "BEGIN", 1)
		end = strings.Replace(marker, "{mark}", "END", 1)
	} else {
		begin = marker + " BEGIN"
		end = marker + " END"
	}
	block := begin + "\n" + line + "\n" + end
	lines := strings.Split(current, "\n")
	beginIdx := -1
	for i, existing := range lines {
		if strings.TrimRight(existing, "\r") == begin {
			beginIdx = i
			break
		}
	}
	if beginIdx >= 0 {
		endIdx := -1
		for i := beginIdx + 1; i < len(lines); i++ {
			if strings.TrimRight(lines[i], "\r") == end {
				endIdx = i
				break
			}
		}
		var tail []string
		if endIdx >= 0 {
			tail = lines[endIdx+1:]
		}
		var next []string
		next = append(next, lines[:beginIdx]...)
		next = append(next, strings.Split(block, "\n")...)
		next = append(next, tail...)
		return strings.Join(next, "\n")
	}
	prefix := current
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return prefix + block + "\n"
}

// WriteFileIfChanged writes content to a remote file only when content/mode/owner differ.
// Returns true when the host would change (or did change).
func (d *Daffodil) WriteFileIfChanged(path string, content string, mode string, owner string) (bool, error) {
	target := d.remoteResolvePath(path)
	matches, err := d.remoteFileMatches(target, content)
	if err != nil {
		return false, err
	}
	modeOk := true
	ownerOk := true
	if mode != "" || owner != "" {
		st, err := d.remoteFileStat(target)
		if err != nil {
			return false, err
		}
		if st == nil {
			modeOk = false
			ownerOk = false
		} else {
			if mode != "" {
				desired, convErr := strconv.ParseInt(mode, 8, 32)
				have, convErr2 := strconv.ParseInt(st["mode"], 8, 32)
				modeOk = convErr == nil && convErr2 == nil && int(desired) == int(have)
			}
			if owner != "" {
				wantUser := ""
				wantGroup := ""
				if idx := strings.Index(owner, ":"); idx >= 0 {
					wantUser = owner[:idx]
					wantGroup = owner[idx+1:]
				} else {
					wantUser = owner
				}
				haveOwner := st["owner"]
				if wantGroup != "" {
					ownerOk = haveOwner == owner
				} else {
					ownerOk = strings.SplitN(haveOwner, ":", 2)[0] == wantUser
				}
			}
		}
	}
	if matches && modeOk && ownerOk {
		return false, nil
	}
	if d.check {
		return true, nil
	}
	if !matches {
		if err := d.writeRemoteFile(target, content); err != nil {
			return false, err
		}
	}
	if mode != "" && !modeOk {
		if _, err := d.sshRunFn(fmt.Sprintf("chmod %s %s", shellQuote(mode), shellQuote(target))); err != nil {
			return false, fmt.Errorf("failed to set mode on %s: %w", target, err)
		}
	}
	if owner != "" && !ownerOk {
		if _, err := d.sshRunFn(fmt.Sprintf("chown %s %s", shellQuote(owner), shellQuote(target))); err != nil {
			return false, fmt.Errorf("failed to set owner on %s: %w", target, err)
		}
	}
	return true, nil
}

// EnsurePackage ensures a system package is installed.
// Returns true when the host would change (or did change).
func (d *Daffodil) EnsurePackage(name string) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, errors.New("ensure package requires a package name")
	}
	manager, err := d.detectPackageManager()
	if err != nil {
		return false, err
	}
	if manager != "" {
		installed := false
		if manager == "apt" {
			if _, err := d.sshRunFn(fmt.Sprintf("dpkg-query -W -f='%%{Status}' -- %s 2>/dev/null | grep -q 'install ok installed'", shellQuote(name))); err == nil {
				installed = true
			}
		} else if manager == "rpm" {
			if _, err := d.sshRunFn(fmt.Sprintf("rpm -q -- %s >/dev/null 2>&1", shellQuote(name))); err == nil {
				installed = true
			}
		} else if manager == "apk" {
			if _, err := d.sshRunFn(fmt.Sprintf("apk info -e -- %s >/dev/null 2>&1", shellQuote(name))); err == nil {
				installed = true
			}
		} else {
			if _, err := d.sshRunFn(fmt.Sprintf("brew list --formula %s >/dev/null 2>&1 || brew list --cask %s >/dev/null 2>&1", shellQuote(name), shellQuote(name))); err == nil {
				installed = true
			}
		}
		if installed {
			return false, nil
		}
	}
	if d.check {
		return true, nil
	}
	installCmd := ""
	switch manager {
	case "apt":
		installCmd = fmt.Sprintf("export DEBIAN_FRONTEND=noninteractive; apt-get update -qq >/dev/null 2>&1 || true; apt-get install -y -q -- %s", shellQuote(name))
	case "rpm":
		installCmd = fmt.Sprintf("if command -v dnf >/dev/null 2>&1; then dnf install -y %s; else yum install -y %s; fi", shellQuote(name), shellQuote(name))
	case "apk":
		installCmd = fmt.Sprintf("apk add --no-cache %s", shellQuote(name))
	case "":
		return false, errors.New("no supported package manager found on the remote host")
	default:
		installCmd = fmt.Sprintf("brew install %s", shellQuote(name))
	}
	if _, err := d.sshRunFn(installCmd); err != nil {
		return false, fmt.Errorf("failed to install package %s: %w", name, err)
	}
	return true, nil
}

func (d *Daffodil) detectPackageManager() (string, error) {
	cmd := "if command -v dpkg-query >/dev/null 2>&1 && command -v apt-get >/dev/null 2>&1; then echo apt; elif command -v rpm >/dev/null 2>&1; then echo rpm; elif command -v apk >/dev/null 2>&1; then echo apk; elif command -v brew >/dev/null 2>&1; then echo brew; fi"
	out, err := d.sshRunFn(cmd)
	if err != nil {
		return "", err
	}
	mgr := strings.TrimSpace(out)
	if mgr == "" {
		return "", errors.New("no supported package manager found on the remote host")
	}
	return mgr, nil
}

// EnsureUser ensures a system user exists.
// Returns true when the host would change (or did change).
func (d *Daffodil) EnsureUser(name string, system bool) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, errors.New("ensure user requires a user name")
	}
	esc := shellQuote(name)
	if _, err := d.sshRunFn(fmt.Sprintf("id -u %s >/dev/null 2>&1", esc)); err == nil {
		return false, nil
	}
	if d.check {
		return true, nil
	}
	systemFlag := ""
	if system {
		systemFlag = " --system"
	}
	cmd := fmt.Sprintf(
		"if command -v useradd >/dev/null 2>&1; then useradd%s -m %s || useradd%s %s; elif command -v adduser >/dev/null 2>&1; then adduser -D %s 2>/dev/null || adduser --disabled-password --gecos  %s; else echo 'no user management tool available' >&2; exit 1; fi",
		systemFlag, esc, systemFlag, esc, esc, esc,
	)
	if _, err := d.sshRunFn(cmd); err != nil {
		return false, fmt.Errorf("failed to create user %s: %w", name, err)
	}
	return true, nil
}

// EnsureServiceEnabled ensures a service is enabled (or disabled).
// Returns true when the host would change (or did change).
func (d *Daffodil) EnsureServiceEnabled(name string, enabled bool) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, errors.New("ensure service enabled requires a service name")
	}
	esc := shellQuote(name)
	mgrOut, err := d.sshRunFn("if command -v systemctl >/dev/null 2>&1; then echo systemctl; elif command -v rc-update >/dev/null 2>&1; then echo openrc; fi")
	if err != nil {
		return false, err
	}
	manager := strings.TrimSpace(mgrOut)
	if manager == "" {
		return false, errors.New("no supported service manager found on the remote host")
	}
	var currently bool
	if manager == "systemctl" {
		out, err := d.sshRunFn(fmt.Sprintf("systemctl is-enabled %s 2>/dev/null", esc))
		if err == nil {
			currently = strings.HasPrefix(strings.TrimSpace(out), "enabled")
		}
	} else {
		if _, err := d.sshRunFn(fmt.Sprintf("rc-update show 2>/dev/null | grep -qw -- %s", esc)); err == nil {
			currently = true
		}
	}
	if currently == enabled {
		return false, nil
	}
	if d.check {
		return true, nil
	}
	var cmd string
	if manager == "systemctl" {
		action := "enable"
		if !enabled {
			action = "disable"
		}
		cmd = fmt.Sprintf("systemctl %s %s", action, esc)
	} else {
		action := "add"
		if !enabled {
			action = "del"
		}
		cmd = fmt.Sprintf("rc-update %s %s", action, esc)
	}
	if _, err := d.sshRunFn(cmd); err != nil {
		return false, fmt.Errorf("failed to %s service %s: %w", map[bool]string{true: "enable", false: "disable"}[enabled], name, err)
	}
	return true, nil
}

// EnsureDirectory ensures a remote directory exists (mkdir -p semantics with change detection).
// Returns true when the host would change (or did change).
func (d *Daffodil) EnsureDirectory(dirPath string) (bool, error) {
	target := d.remoteResolvePath(dirPath)
	if _, err := d.sshRunFn(fmt.Sprintf("[ -d %s ]", shellQuote(target))); err == nil {
		return false, nil
	}
	if d.check {
		return true, nil
	}
	if _, err := d.sshRunFn(fmt.Sprintf("mkdir -p %s", shellQuote(target))); err != nil {
		return false, fmt.Errorf("failed to create directory %s: %w", target, err)
	}
	return true, nil
}

func (d *Daffodil) TransferFiles(localPath string, destinationPath string) (bool, error) {
	if strings.TrimSpace(localPath) == "" {
		return false, errors.New("local path must not be empty")
	}
	if destinationPath == "" {
		destinationPath = d.remotePath
	}

	info, err := os.Stat(localPath)
	if err != nil {
		return false, fmt.Errorf("stat local path: %w", err)
	}

	body, hash, fileCount, err := d.localManifest(localPath, info.IsDir())
	if err != nil {
		return false, err
	}
	remoteManifestPath := d.remoteManifestPath(destinationPath)
	storedHash, err := d.readStoredManifest(remoteManifestPath)
	if err != nil {
		return false, err
	}
	if storedHash != "" && storedHash == hash {
		return false, nil
	}
	if d.check {
		return true, nil
	}

	tmpArchive := filepath.Join(os.TempDir(), fmt.Sprintf("godaffodil-%d.tar.gz", time.Now().UnixNano()))
	defer func() {
		_ = os.Remove(tmpArchive)
	}()

	if err := createTarGz(tmpArchive, localPath, info.IsDir(), d.excludeList); err != nil {
		return false, fmt.Errorf("create archive: %w", err)
	}

	remoteArchive := remoteJoin(destinationPath, filepath.Base(tmpArchive))
	if err := d.scpFn(tmpArchive, remoteArchive); err != nil {
		return false, fmt.Errorf("scp upload failed: %w", err)
	}

	extractCmd := fmt.Sprintf(
		"mkdir -p %q && cd %q && tar -xzf %q && rm -f %q",
		destinationPath, destinationPath, remoteArchive, remoteArchive,
	)
	if _, err := d.sshRunFn(extractCmd); err != nil {
		return false, fmt.Errorf("remote extract failed: %w", err)
	}

	if err := d.writeStoredManifest(remoteManifestPath, hash, fileCount, body); err != nil {
		return false, err
	}
	return true, nil
}

func (d *Daffodil) localManifest(localPath string, isDir bool) (string, string, int, error) {
	var entries []string
	if isDir {
		err := filepath.WalkDir(localPath, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			_, statErr := entry.Info()
			if statErr != nil {
				return statErr
			}
			if shouldExclude(path, localPath, d.excludeList) {
				return nil
			}
			rel, relErr := filepath.Rel(localPath, path)
			if relErr != nil {
				return relErr
			}
			hash, hashErr := fileSHA256(path)
			if hashErr != nil {
				return hashErr
			}
			entries = append(entries, fmt.Sprintf("%s  %s", hash, filepath.ToSlash(rel)))
			return nil
		})
		if err != nil {
			return "", "", 0, fmt.Errorf("walk local path: %w", err)
		}
	} else {
		hash, err := fileSHA256(localPath)
		if err != nil {
			return "", "", 0, err
		}
		entries = append(entries, fmt.Sprintf("%s  %s", hash, filepath.Base(localPath)))
	}
	sort.Strings(entries)
	body := ""
	if len(entries) > 0 {
		body = strings.Join(entries, "\n") + "\n"
	}
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	return body, hash, len(entries), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (d *Daffodil) remoteManifestPath(destinationPath string) string {
	return remoteJoin(destinationPath, ".daffodil-manifest")
}

func (d *Daffodil) readStoredManifest(path string) (string, error) {
	cmd := fmt.Sprintf("if [ -f %s ]; then cat %s; fi", shellQuote(path), shellQuote(path))
	out, err := d.sshRunFn(cmd)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() != 0 {
			return "", nil
		}
		return "", fmt.Errorf("read remote manifest %s: %w", path, err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 || lines[0] != "daffodil-manifest-v1" {
		return "", nil
	}
	digest := strings.TrimSpace(lines[1])
	if !isSHA256Hex(digest) {
		return "", nil
	}
	return digest, nil
}

func (d *Daffodil) writeStoredManifest(path string, hash string, count int, body string) error {
	writeCmd := fmt.Sprintf("cat > %s <<'DAFFODIL_EOF'\ndaffodil-manifest-v1\n%s\n%d\n%s\nDAFFODIL_EOF", shellQuote(path), hash, count, body)
	if _, err := d.sshRunFn(writeCmd); err != nil {
		return fmt.Errorf("write remote manifest %s: %w", path, err)
	}
	return nil
}

// Deploy executes steps sequentially and stops on first error.
func (d *Daffodil) Deploy(steps []Step) error {
	_, err := d.DeployWithResults(steps)
	if err != nil {
		return err
	}
	return nil
}

// DeployWithResults executes steps sequentially and returns structured step results.
// It stops on the first failed step and returns the collected results alongside the error.
func (d *Daffodil) DeployWithResults(steps []Step) ([]StepResult, error) {
	if len(d.targets) > 0 {
		var firstErr error
		for i := range d.targets {
			target := &d.targets[i]
			prevUser, prevHost, prevPort := d.remoteUser, d.remoteHost, d.port
			d.remoteUser, d.remoteHost = target.User, target.Host
			if target.Port > 0 {
				d.port = target.Port
			}
			if d.verbose {
				fmt.Printf("==== Deploying to [%s] (%s) ====\n", target.Name, target.Host)
			}
			_, err := d.deployWithResultsInternal(steps)
			d.remoteUser, d.remoteHost, d.port = prevUser, prevHost, prevPort
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return nil, firstErr
			}
		}
		return []StepResult{}, nil
	}
	return d.deployWithResultsInternal(steps)
}

func (d *Daffodil) deployWithResultsInternal(steps []Step) ([]StepResult, error) {
	if len(steps) == 0 {
		return nil, errors.New("deploy requires at least one step")
	}

	results := []StepResult{}
	anyChanged := false
	failedIndex := -1
	failedName := ""
	var failedErr error

	for i := range steps {
		step := &steps[i]
		name := step.Name
		status := "ok"
		changed := false

		if step.When == "changed" && !anyChanged {
			status = "skipped"
		} else {
			if step.StateCommand != nil {
				var err error
				changed, err = step.StateCommand()
				if err != nil {
					failedIndex, failedName, failedErr = i, step.Name, err
					status = "failed"
					results = append(results, StepResult{Name: name, Status: "failed", Changed: false})
					break
				}
			} else if step.Command != nil {
				if err := step.Command(); err != nil {
					failedIndex, failedName, failedErr = i, step.Name, err
					status = "failed"
					results = append(results, StepResult{Name: name, Status: "failed", Changed: false})
					break
				}
			} else {
				failedIndex, failedName, failedErr = i, step.Name, errors.New("step has no command")
				status = "failed"
				results = append(results, StepResult{Name: name, Status: "failed", Changed: false})
				break
			}
			if changed {
				anyChanged = true
				if d.check {
					status = "would-change"
				} else {
					status = "changed"
				}
			}
		}

		results = append(results, StepResult{Name: name, Status: status, Changed: changed})
	}

	if failedErr != nil {
		return results, fmt.Errorf("deploy failed at step %d (%s): %w", failedIndex+1, failedName, failedErr)
	}
	return results, nil
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
	return d.scpFn(localPath, remotePath)
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

// LoadInventoryTargets parses an Ansible-style inventory.ini and returns hosts for the given group.
// If group is empty, hosts from all sections are returned.
func LoadInventoryTargets(path, group string) ([]InventoryTarget, error) {
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
