package godaffodil

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Config configures a Daffodil deployment client.
type Config struct {
	RemoteUser string
	RemoteHost string
	RemotePath string
	Port       int
	SSHKeyPath string
	Verbose    bool
}

// Step defines one deployment step.
type Step struct {
	Name    string
	Command func() error
}

// Daffodil is a lightweight deployment helper for SSH/SCP workflows.
type Daffodil struct {
	remoteUser string
	remoteHost string
	remotePath string
	port       int
	sshKeyPath string
	verbose    bool
}

// New creates a new deployment client.
func New(cfg Config) (*Daffodil, error) {
	if strings.TrimSpace(cfg.RemoteUser) == "" {
		return nil, errors.New("remote user is required")
	}
	if strings.TrimSpace(cfg.RemoteHost) == "" {
		return nil, errors.New("remote host is required")
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	if cfg.RemotePath == "" {
		cfg.RemotePath = "."
	}

	return &Daffodil{
		remoteUser: cfg.RemoteUser,
		remoteHost: cfg.RemoteHost,
		remotePath: cfg.RemotePath,
		port:       cfg.Port,
		sshKeyPath: cfg.SSHKeyPath,
		verbose:    cfg.Verbose,
	}, nil
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

	if err := createTarGz(tmpArchive, localPath, info.IsDir()); err != nil {
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

func createTarGz(targetArchive, sourcePath string, sourceIsDir bool) error {
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

func remoteJoin(base, child string) string {
	if strings.HasSuffix(base, "/") {
		return base + strings.TrimPrefix(child, "/")
	}
	return base + "/" + strings.TrimPrefix(child, "/")
}
