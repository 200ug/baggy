package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"codeberg.org/2ug/baggy/internal"
)

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

func cmdSync(args []string) {
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

	// load remote conn from ~/.config/baggy.conf; must be manually init'd if absent
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
		fmt.Println("[+] no remote metadata found (first sync)")
	}

	// 2b) propagate local deletions to remote before diff
	deletions := internal.LocalDeletions(oldLocalMeta, localMeta)
	if len(deletions) > 0 && remoteMeta != nil {
		for _, f := range deletions {
			remotePath := path.Join(remoteConn.Config.StorageRoot, filepath.Base(absRoot), f.LocalPath+"."+internal.FileExt)
			if err = remoteConn.DeleteRemoteFile(remotePath); err != nil {
				fmt.Printf("[!] delete remote %s: %v\n", f.LocalPath, err)
				os.Exit(1)
			}
			// remove from remoteMeta so Diff doesn't re-download it
			filtered := remoteMeta.Files[:0]
			for _, rf := range remoteMeta.Files {
				if rf.LocalPath != f.LocalPath {
					filtered = append(filtered, rf)
				}
			}
			remoteMeta.Files = filtered
			fmt.Printf("[-] %s\n", f.LocalPath)
		}
	}

	// 3) compute diff
	diff := internal.Diff(localMeta, remoteMeta)
	fmt.Printf("[+] diff: upload=%d download=%d\n", len(diff.ToUpload), len(diff.ToDownload))

	// 4-5) derive key once, then [encrypt + push] or [pull + decrypt] per file
	if len(diff.ToUpload)+len(diff.ToDownload) > 0 {
		kh, err := internal.NewKeyHolder(remoteConn.Config.Salt)
		if err != nil {
			fmt.Printf("[!] failed to read password: %v\n", err)
			os.Exit(1)
		}

		for _, f := range diff.ToUpload {
			rel := f.LocalPath
			remotePath := path.Join(remoteConn.Config.StorageRoot, filepath.Base(absRoot), rel+"."+internal.FileExt)

			tmp, err := os.CreateTemp("", "baggy-enc-*")
			if err != nil {
				fmt.Printf("[!] upload %s: create temp: %v\n", rel, err)
				os.Exit(1)
			}
			tmp.Close()

			if err = kh.EncryptFile(filepath.Join(absRoot, f.LocalPath), tmp.Name()); err != nil {
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
			fmt.Printf("[>] %s\n", rel)
		}

		for _, f := range diff.ToDownload {
			rel := f.LocalPath
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
			entry := internal.Filedata{LocalPath: rel, ContentHash: hash, ModifiedAt: info.ModTime().Unix()}
			updated := false
			for i, lf := range localMeta.Files {
				if lf.LocalPath == rel {
					localMeta.Files[i] = entry
					updated = true
					break
				}
			}
			if !updated {
				localMeta.Files = append(localMeta.Files, entry)
			}
			fmt.Printf("[<] %s\n", rel)
		}
	}

	// 6) write updated meta locally and push to remote so both sides are equal
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
