package main

import (
	"flag"
	"fmt"
	"os"
	"path"
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

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Printf("[!] failed to parse absolute root path: %v\n", err)
		os.Exit(1)
	}

	// load remote conn from ~/.config/baggy.conf; must be manually init'd if absent
	remoteConn, err := internal.LoadRemoteConn()
	if err != nil {
		fmt.Println("[!] remote config unavailable (run init to configure)")
		os.Exit(1)
	}
	defer remoteConn.SFTP.Close()
	defer remoteConn.Conn.Close()

	// 1) load local meta or initialize from directory walk
	localMeta, err := internal.NewMetadata(absRoot)
	if err != nil {
		fmt.Printf("[!] failed to load metadata: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[+] local metadata: files=%d\n", len(localMeta.Files))

	// 2) fetch remote metafile (nil == first sync, remote has no state yet)
	remoteMeta, err := remoteConn.PullRemoteMetafile(absRoot)
	if err != nil {
		fmt.Printf("[!] failed to fetch remote metadata: %v\n", err)
		os.Exit(1)
	}
	if remoteMeta != nil {
		fmt.Printf("[+] remote metadata: files=%d\n", len(remoteMeta.Files))
	} else {
		fmt.Println("[+] no remote metadata found (first sync)")
	}

	// 3) compute diff
	diff := internal.Diff(localMeta, remoteMeta, absRoot)
	fmt.Printf("[+] diff: upload=%d download=%d\n", len(diff.ToUpload), len(diff.ToDownload))

	// 4-5) derive key once, then [encrypt + push] or [pull + decrypt] per file
	if len(diff.ToUpload)+len(diff.ToDownload) > 0 {
		kh, err := internal.NewKeyHolder(remoteConn.Config.Salt)
		fmt.Println() // newline after password prompt
		if err != nil {
			fmt.Printf("[!] failed to read password: %v\n", err)
			os.Exit(1)
		}

		for _, f := range diff.ToUpload {
			rel, _ := filepath.Rel(absRoot, f.LocalPath)
			remotePath := path.Join(remoteConn.Config.StorageRoot, filepath.Base(absRoot), rel+"."+internal.FileExt)

			tmp, err := os.CreateTemp("", "baggy-enc-*")
			if err != nil {
				fmt.Printf("[!] upload %s: create temp: %v\n", rel, err)
				os.Exit(1)
			}
			tmp.Close()

			if err = kh.EncryptFile(f.LocalPath, tmp.Name()); err != nil {
				os.Remove(tmp.Name())
				fmt.Printf("[!] upload %s: encrypt: %v\n", rel, err)
				os.Exit(1)
			}
			if err = remoteConn.PushFile(tmp.Name(), remotePath); err != nil {
				os.Remove(tmp.Name())
				fmt.Printf("[!] upload %s: push: %v\n", rel, err)
				os.Exit(1)
			}
			os.Remove(tmp.Name())
			fmt.Printf("[+] uploaded %s\n", rel)
		}

		for _, f := range diff.ToDownload {
			rel, _ := filepath.Rel(absRoot, f.LocalPath)
			remotePath := path.Join(remoteConn.Config.StorageRoot, filepath.Base(absRoot), rel+"."+internal.FileExt)
			dst := filepath.Join(absRoot, rel)

			tmp, err := os.CreateTemp("", "baggy-enc-*")
			if err != nil {
				fmt.Printf("[!] download %s: create temp: %v\n", rel, err)
				os.Exit(1)
			}
			tmp.Close()

			if err = remoteConn.PullFile(remotePath, tmp.Name()); err != nil {
				os.Remove(tmp.Name())
				fmt.Printf("[!] download %s: pull: %v\n", rel, err)
				os.Exit(1)
			}
			if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				os.Remove(tmp.Name())
				fmt.Printf("[!] download %s: mkdir: %v\n", rel, err)
				os.Exit(1)
			}
			if err = kh.DecryptFile(tmp.Name(), dst); err != nil {
				os.Remove(tmp.Name())
				fmt.Printf("[!] download %s: decrypt: %v\n", rel, err)
				os.Exit(1)
			}
			os.Remove(tmp.Name())

			// update local meta entry with the newly written file's hash and modtime
			info, err := os.Stat(dst)
			if err != nil {
				fmt.Printf("[!] download %s: stat: %v\n", rel, err)
				os.Exit(1)
			}
			hash, err := internal.HashFile(dst)
			if err != nil {
				fmt.Printf("[!] download %s: hash: %v\n", rel, err)
				os.Exit(1)
			}
			entry := internal.Filedata{LocalPath: dst, ContentHash: hash, ModifiedAt: info.ModTime().Unix()}
			updated := false
			for i, lf := range localMeta.Files {
				if lf.LocalPath == dst {
					localMeta.Files[i] = entry
					updated = true
					break
				}
			}
			if !updated {
				localMeta.Files = append(localMeta.Files, entry)
			}
			fmt.Printf("[+] downloaded %s\n", rel)
		}
	}

	// 6) write updated meta locally and push to remote so both sides are equal
	metaPath := filepath.Join(absRoot, internal.Metafile)
	if err = localMeta.WriteToFile(metaPath); err != nil {
		fmt.Printf("[!] failed to write local metadata: %v\n", err)
		os.Exit(1)
	}
	if err = remoteConn.PushRemoteMetafile(absRoot, localMeta); err != nil {
		fmt.Printf("[!] failed to push remote metadata: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[+] metadata synced")
}
