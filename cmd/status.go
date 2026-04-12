package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"codeberg.org/2ug/wsftp/internal"
)

func CmdStatus(args []string) {
	fmt.Println("[~] performing a dry-run (no local or remote changes will be made)")

	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	root := fs.String("root", ".", "root directory to sync")
	skipBins := fs.Bool("skipbins", false, "exclude certain binaries to save bandwidth")
	fs.Parse(args)

	internal.SkipBinaryFiles = *skipBins // promote to internal's global

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Printf("[!] failed to parse absolute root path: %v\n", err)
		os.Exit(1)
	}

	// load remote conn from ~/.config/wsftp.conf; must be manually init'd if absent
	remoteConn, err := internal.LoadRemoteConn()
	if err != nil {
		fmt.Println("[!] remote config unavailable (run init to configure)")
		os.Exit(1)
	}
	defer remoteConn.SFTP.Close()
	defer remoteConn.Conn.Close()

	metaPath := filepath.Join(absRoot, internal.Metafile)

	// read old metafile as baseline for deletion detection (nil on first sync)
	var oldLocalMeta *internal.Metadata
	if raw, readErr := os.ReadFile(metaPath); readErr == nil {
		var m internal.Metadata
		if json.Unmarshal(raw, &m) == nil {
			oldLocalMeta = &m
		}
	}

	// 1) load local meta or initialize from file walk
	localMeta, err := internal.NewMetadata(absRoot)
	if err != nil {
		fmt.Printf("[!] failed to load metadata: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[+] local metadata: files=%d\n", len(localMeta.Files))

	// 2a) fetch remote metafile (nil == first sync, remote has no state yet)
	remoteMeta, err := remoteConn.PullRemoteMetafile(absRoot)
	if err != nil {
		fmt.Printf("[!] failed to fetch remote metadata: %v\n", err)
		os.Exit(1)
	}
	if remoteMeta != nil {
		fmt.Printf("[+] remote metadata: files=%d\n", len(remoteMeta.Files))
	} else {
		fmt.Println("[+] no remote metadata found (all non-filtered files will be uploaded)")
		return // stop right here as we're just dry-running
	}

	// 2b) display local deletions which would be propagated to remote
	deletions := internal.LocalDeletions(oldLocalMeta, localMeta)
	if len(deletions) > 0 {
		fmt.Println("[-] remote deletions:")
		for _, f := range deletions {
			fmt.Printf("\t%s\n", f.LocalPath)
		}
	}

	// 3) compute diff and print out the changes item by item
	diff := internal.Diff(localMeta, remoteMeta)
	if len(diff.ToUpload) > 0 {
		fmt.Println("[>] uploaded to remote:")
		for _, d := range diff.ToUpload {
			fmt.Printf("\t%s\n", d.LocalPath)
		}
	} else {
		fmt.Println("[+] no changes to upload to remote")
	}
	if len(diff.ToDownload) > 0 {
		fmt.Println("[<] downloaded from remote:")
		for _, d := range diff.ToDownload {
			fmt.Printf("\t%s\n", d.LocalPath)
		}
	} else {
		fmt.Println("[+] no changes to download from remote")
	}
}
