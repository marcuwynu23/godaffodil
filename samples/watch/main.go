package main

import (
	"fmt"
	"log"

	"github.com/marcuwynu23/godaffodil"
)

func main() {
	remotePath := "/user/test"
	deployer, err := godaffodil.New(godaffodil.Config{
		RemoteUser: "user",
		RemoteHost: "ssh.server.com",
		RemotePath: remotePath,
		Verbose:    false,
	})
	if err != nil {
		log.Fatal(err)
	}

	steps := []godaffodil.Step{
		{
			Name: "Build project",
			Command: func() error {
				return deployer.Local("npm run build")
			},
		},
		{
			Name: "Upload build",
			Command: func() error {
				return deployer.TransferFiles("./dist", remotePath)
			},
		},
		{
			Name: "Restart app",
			Command: func() error {
				return deployer.SSHCommand("pm2 restart myapp")
			},
		},
	}

	err = deployer.Watch(godaffodil.WatchOptions{
		Paths:      []string{"./dist", "./src"},
		DebounceMS: 2000,
		RepoPath:   ".",
		Branch:     "main",
		Tags:       true,
		Events:     []string{"commit", "merge", "tag"},
		IntervalMS: 5000,
	}).Deploy(steps)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("watch() is active. Edit files or push commits to trigger deployments.")
	select {}
}
