package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

type fakeSSH struct {
	files     map[string]string
	stats     map[string]map[string]string
	dirs      map[string]bool
	users     map[string]bool
	packages  map[string]bool
	services  map[string]bool
	commands  []string
	mutations int
	home      string
}

func newFakeSSH() *fakeSSH {
	return &fakeSSH{
		files:    map[string]string{},
		stats:    map[string]map[string]string{},
		dirs:     map[string]bool{},
		users:    map[string]bool{},
		packages: map[string]bool{},
		services: map[string]bool{},
		home:     "/home/deployer",
	}
}

func (f *fakeSSH) scp(localPath string, remotePath string) error {
	return nil
}

func exitErrOf(code int) error {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

var (
	reCat       = regexp.MustCompile(`^if \[ -f '([^']+)' \]; then cat '([^']+)'; else exit 3; fi$`)
	reSha256    = regexp.MustCompile(`^if \[ -f '([^']+)' \]; then \(sha256sum`)
	reStat      = regexp.MustCompile(`^if \[ -f '([^']+)' \]; then \(stat -c '([^']*)`)
	reChmod     = regexp.MustCompile(`^chmod '([^']+)' '([^']+)'$`)
	reChown     = regexp.MustCompile(`^chown '([^']+)' '([^']+)'$`)
	reDir       = regexp.MustCompile(`^\[ -d '([^']+)' \]$`)
	reMkdir     = regexp.MustCompile(`^mkdir -p '([^']+)'`)
	reIdUser    = regexp.MustCompile(`^id -u '([^']+)' >/dev/null 2>&1$`)
	reMgrApt    = regexp.MustCompile(`echo apt`)
	reMgrSvc    = regexp.MustCompile(`echo systemctl`)
	rePkg       = regexp.MustCompile(`dpkg-query -W`)
	reAptGet    = regexp.MustCompile(`apt-get install`)
	reIsEnabled = regexp.MustCompile(`^systemctl is-enabled '([^']+)' 2>/dev/null$`)
	reSvcAct    = regexp.MustCompile(`^systemctl (enable|disable) '([^']+)'$`)
	reWhich     = regexp.MustCompile(`^which (\S+) 2>/dev/null$`)
)

func (f *fakeSSH) run(command string) (string, error) {
	f.commands = append(f.commands, command)
	if command == "printf '%s' \"$HOME\"" {
		return f.home, nil
	}
	if strings.HasPrefix(command, "cat > ") {
		rest := strings.TrimPrefix(command, "cat > ")
		m := regexp.MustCompile(`^'([^']+)'`).FindStringSubmatch(rest)
		path := ""
		content := ""
		if m != nil {
			path = m[1]
		}
		idx := strings.Index(command, "<<'DAFFODIL_EOF")
		endIdx := strings.Index(command, "\nDAFFODIL_EOF")
		if idx >= 0 && endIdx > idx {
			content = command[idx+len("<<'DAFFODIL_EOF") : endIdx]
			f.files[path] = content
		}
		return "", nil
	}
	if m := reCat.FindStringSubmatch(command); m != nil {
		content, ok := f.files[m[2]]
		if !ok {
			return "", exitErrOf(3)
		}
		return content, nil
	}
	if strings.Contains(command, "sha256sum") || strings.Contains(command, "shasum") || strings.Contains(command, "openssl dgst") {
		m := reSha256.FindStringSubmatch(command)
		if m == nil {
			return "", nil
		}
		content, ok := f.files[m[1]]
		if !ok {
			return "", exitErrOf(3)
		}
		return sha256Hex(content), nil
	}
	if strings.Contains(command, "stat -c") {
		m := reStat.FindStringSubmatch(command)
		if m == nil {
			return "", nil
		}
		st, ok := f.stats[m[1]]
		if !ok {
			return "", exitErrOf(3)
		}
		owner := st["owner"]
		if owner == "" {
			owner = "deployer:deployer"
		}
		return st["mode"] + " " + owner, nil
	}
	if m := reChmod.FindStringSubmatch(command); m != nil {
		st := f.stats[m[2]]
		if st == nil {
			st = map[string]string{"mode": "644", "owner": "deployer:deployer"}
			f.stats[m[2]] = st
		}
		st["mode"] = m[1]
		f.mutations++
		return "", nil
	}
	if m := reChown.FindStringSubmatch(command); m != nil {
		st := f.stats[m[2]]
		if st == nil {
			st = map[string]string{"mode": "644", "owner": "deployer:deployer"}
			f.stats[m[2]] = st
		}
		st["owner"] = m[1]
		f.mutations++
		return "", nil
	}
	if m := reDir.FindStringSubmatch(command); m != nil {
		if f.dirs[m[1]] {
			return "", nil
		}
		f.dirs[m[1]] = true
		return "", nil
	}
	if m := reMkdir.FindStringSubmatch(command); m != nil {
		if !f.dirs[m[1]] {
			f.dirs[m[1]] = true
			f.mutations++
		}
		return "", nil
	}
	if m := reIdUser.FindStringSubmatch(command); m != nil {
		if f.users[m[1]] {
			return "", nil
		}
		return "", exitErrOf(1)
	}
	if strings.Contains(command, "useradd") || strings.Contains(command, "adduser") {
		f.users["deployer2"] = true
		return "", nil
	}
	if reMgrApt.MatchString(command) {
		return "apt\n", nil
	}
	if reMgrSvc.MatchString(command) {
		return "systemctl\n", nil
	}
	if rePkg.MatchString(command) {
		return "", nil
	}
	if reAptGet.MatchString(command) {
		f.mutations++
		return "", nil
	}
	if m := reIsEnabled.FindStringSubmatch(command); m != nil {
		if f.services[m[1]] {
			return "enabled\n", nil
		}
		return "", exitErrOf(1)
	}
	if m := reSvcAct.FindStringSubmatch(command); m != nil {
		if m[1] == "enable" {
			f.services[m[2]] = true
		} else {
			delete(f.services, m[2])
		}
		f.mutations++
		return "", nil
	}
	if strings.HasPrefix(command, "uname") {
		return "Linux\n", nil
	}
	if m := reWhich.FindStringSubmatch(command); m != nil {
		if m[1] == "tar" || m[1] == "gzip" {
			return "/usr/bin/" + m[1] + "\n", nil
		}
		return "", exitErrOf(1)
	}
	return "", nil
}

func newTestDeployer(check bool) *Daffodil {
	d, err := New(Config{
		RemoteUser: "deployer",
		RemoteHost: "127.0.0.1",
		RemotePath: "/home/deployer",
		Check:      check,
	})
	if err != nil {
		panic(err)
	}
	fake := newFakeSSH()
	d.sshRunFn = fake.run
	d.scpFn = fake.scp
	return d
}

func TestEnsureLineInFileIdempotent(t *testing.T) {
	d := newTestDeployer(false)
	first, err := d.EnsureLineInFile("/home/deployer/.bashrc", "export APP=1", "")
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if !first {
		t.Fatal("expected first run to change")
	}
	second, err := d.EnsureLineInFile("/home/deployer/.bashrc", "export APP=1", "")
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if second {
		t.Fatal("expected second run to be idempotent")
	}
}

func TestDeployWhenChangedSkipsRestart(t *testing.T) {
	d := newTestDeployer(false)
	steps := []Step{
		{Name: "Line", StateCommand: func() (bool, error) { return d.EnsureLineInFile("/home/deployer/.bashrc", "export APP=1", "") }},
		{Name: "Restart", When: "changed", Command: func() error { return nil }},
	}
	results, err := d.DeployWithResults(steps)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Status != "changed" {
		t.Fatalf("expected line status changed, got %s", results[0].Status)
	}
	if results[1].Status != "ok" {
		t.Fatalf("expected restart status ok, got %s", results[1].Status)
	}
}

func TestDeployCheckModeWouldChange(t *testing.T) {
	d := newTestDeployer(true)
	steps := []Step{
		{Name: "Line", StateCommand: func() (bool, error) { return d.EnsureLineInFile("/home/deployer/.bashrc", "export APP=1", "") }},
	}
	results, err := d.DeployWithResults(steps)
	if err != nil {
		t.Fatalf("deploy check: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "would-change" {
		t.Fatalf("expected would-change, got %s", results[0].Status)
	}
}
