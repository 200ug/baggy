package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"codeberg.org/2ug/baggy/internal"
)

/*
	1. load local meta file (or initialize it)
	2. fetch server meta file via sftp
	3. compute diff (merged key walk)
	4. for each file to upload: read plaintext -> hash -> encrypt to a temp file -> sftp put -> update local meta entry
	5. for each file to download: sftp get -> decrypt -> write locally -> update local meta entry
	6. write updated meta file locally *and* push it to the server (to make sure they're equal)
	7. the meta file write should be atomic on both ends (i.e. write to a temp file, then rename), prevents half-written file if the proc crashes
*/

const usage = `usage: baggy <command> [flags]

commands:
  init  configure remote and verify connectivity
  sync  run the sync workflow against the configured remote

`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		cmdInit(os.Args[2:])
	case "sync":
		cmdSync(os.Args[2:])
	default:
		fmt.Print(usage)
		os.Exit(1)
	}
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	compact := fs.String("remote", "", "remote in the form <user>@<hostname>:<port>:<storage_root>")
	privKey := fs.String("key", "", "path to ssh private key")
	fs.Parse(args)

	if *compact == "" || *privKey == "" {
		fmt.Println("[!] -remote and -key are required")
		fs.Usage()
		os.Exit(1)
	}

	if _, err := internal.NewRemoteConn(*compact, *privKey, false); err != nil {
		fmt.Printf("[!] init failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[+] remote configured and verified")
}

func cmdSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	root := fs.String("root", ".", "root directory to sync")
	fs.Parse(args)

	// load remote conn from ~/.config/baggy.conf; must be manually init'd if absent
	remoteConn, err := internal.LoadRemoteConn()
	if err != nil {
		fmt.Println("[!] remote config unavailable (run init to configure)")
		os.Exit(1)
	}
	defer remoteConn.SFTP.Close()
	defer remoteConn.Conn.Close()

	// 1) load local meta or initialize from directory walk
	meta, err := internal.NewMetadata(*root, remoteConn)
	if err != nil {
		fmt.Printf("[!] failed to load metadata: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[+] local metadata: files=%d\n", len(meta.Files))

	// 2-5) not yet implemented (sftp ops, diff, crypto)

	// 6 [local half]) write updated meta back to disk
	metaPath := filepath.Join(*root, internal.Metafile)
	if err = meta.WriteToFile(metaPath); err != nil {
		fmt.Printf("[!] failed to write metadata: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[+] metadata written to %s\n", metaPath)
}
