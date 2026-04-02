package main

import (
	"fmt"
	"log"

	"github.com/marcuwynu23/godaffodil"
)

func main() {
	deployer, err := godaffodil.New(godaffodil.Config{
		Inventory:  "./inventory.ini",
		Group:      "webservers",
		RemotePath: "/",
		Verbose:    false,
	})
	if err != nil {
		log.Fatal(err)
	}

	steps := []godaffodil.Step{
		{
			Name: "Install dependencies",
			Command: func() error {
				return deployer.SSHCommand("ls")
			},
		},
	}

	if err := deployer.Deploy(steps); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Multi-host deployment finished successfully! (Go)")
}
