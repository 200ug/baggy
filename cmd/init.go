package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"codeberg.org/2ug/wsftp/internal"
)

func CmdInit(args []string) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("[!] user home could not be resolved: %s\n", err)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("init", flag.ExitOnError)
	compact := fs.String("remote", "", "remote in the form <user>@<hostname>:<port>:<storage_root>")
	privKey := fs.String("key", "", "path to ssh private key")
	knownHosts := fs.String("knownhosts", filepath.Join(userHome, ".ssh/known_hosts"), "path to ssh known hosts file")
	fs.Parse(args)

	if *compact == "" || *privKey == "" {
		fmt.Println("[!] -remote and -key are required")
		fs.Usage()
		os.Exit(1)
	}

	if _, err := internal.NewRemoteConn(*compact, *privKey, *knownHosts, false); err != nil {
		fmt.Printf("[!] init failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[+] remote configured and verified")
}
