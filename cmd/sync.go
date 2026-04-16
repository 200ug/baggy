package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"codeberg.org/2ug/wsftp/internal"
)

func CmdSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	root := fs.String("root", ".", "root directory to sync")
	skipBins := fs.Bool("skipbins", false, "exclude certain binaries to save bandwidth")
	fs.Parse(args)

	internal.SkipBinaryFiles = *skipBins // promote to internal's global namespace

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

	// 1) load local metadata from a metafile (if present) and initialize a fresh version from file walk
	//	  -> local deletions are detected from the difference between the old and new metafiles
	var oldLocalMeta *internal.Metadata
	if raw, readErr := os.ReadFile(metaPath); readErr == nil {
		var m internal.Metadata
		if json.Unmarshal(raw, &m) == nil {
			oldLocalMeta = &m
		}
	}
	localMeta, err := internal.NewMetadata(absRoot)
	if err != nil {
		fmt.Printf("[!] failed to load metadata: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[+] local metadata: files=%d\n", len(localMeta.Files))

	// 2) fetch remote metafile (nil if this is the first sync and no remote state exists yet)
	remoteMeta, err := remoteConn.PullRemoteMetafile(absRoot)
	if err != nil {
		fmt.Printf("[!] failed to fetch remote metadata: %v\n", err)
		os.Exit(1)
	}
	isFirstSync := remoteMeta == nil
	if isFirstSync {
		fmt.Println("[+] no remote metadata found (first sync)")
	} else {
		fmt.Printf("[+] remote metadata: files=%d\n", len(remoteMeta.Files))
	}

	// 3) propagate local deletions to remote before diff
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

	// remote maintenance: push the salt if it has (accidentally) been deleted
	if _, err := remoteConn.SFTP.Stat(path.Join(remoteConn.Config.StorageRoot, "salt")); os.IsNotExist(err) {
		if err := remoteConn.PushSalt(remoteConn.Config.Salt); err != nil {
			fmt.Printf("[!] failed to push salt to remote: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[+] restored salt to remote")
	}

	// 4) check if we have an existing verification file somewhere or propagate the just created file to remote
	remoteVerifPath := path.Join(remoteConn.Config.StorageRoot, filepath.Base(absRoot), internal.VerificationFile)
	localVerifPath := filepath.Join(absRoot, internal.VerificationFile)
	if _, err := os.Stat(localVerifPath); os.IsNotExist(err) {
		if !isFirstSync {
			if err := remoteConn.PullFile(remoteVerifPath, localVerifPath); err != nil {
				fmt.Printf("[!] failed to fetch verification file from remote: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("[+] fetched verification file from remote")
		}
	} else if err != nil {
		fmt.Printf("[!] failed to stat verification file: %v\n", err)
		os.Exit(1)
	}

	// 5) compute diff (between local and remote)
	diff := internal.Diff(localMeta, remoteMeta)
	fmt.Printf("[+] diff: upload=%d download=%d\n", len(diff.ToUpload), len(diff.ToDownload))

	// 6) derive key once, then [encrypt + push] or [pull + decrypt] per file
	totalJobs := len(diff.ToUpload) + len(diff.ToDownload)
	if totalJobs > 0 {
		kh, err := internal.NewKeyHolder(remoteConn.Config.Salt, absRoot, isFirstSync)
		if err != nil {
			fmt.Printf("[!] failed to read password: %v\n", err)
			os.Exit(1)
		}
		pool := internal.NewSyncWorkerPool(kh, remoteConn, absRoot)

		// push the verification file to remote if it was just created
		if isFirstSync {
			if err = remoteConn.PushFile(localVerifPath, remoteVerifPath); err != nil {
				fmt.Printf("[!] failed to push verification file to remote: %v\n", err)
				os.Exit(1)
			}
		}

		// v1.3: handle all diffs inside the worker pool
		metaResults, err := pool.StartWorkers(diff, totalJobs)
		if err != nil {
			fmt.Printf("[!] sync failed: %v\n", err)
			os.Exit(1)
		}

		for _, entry := range metaResults {
			updated := false
			for i, lf := range localMeta.Files {
				if lf.LocalPath == entry.LocalPath {
					localMeta.Files[i] = entry
					updated = true
					break
				}
			}
			if !updated {
				localMeta.Files = append(localMeta.Files, entry)
			}
		}
	}

	// 7) sync meta state between local and remote (local always overrides remote)
	if err = localMeta.WriteToFile(metaPath); err != nil {
		fmt.Printf("[!] failed to write local metadata: %v\n", err)
		os.Exit(1)
	}
	if err = remoteConn.PushMetafileRemote(absRoot, localMeta); err != nil {
		fmt.Printf("[!] failed to push metadata to remote: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[+] metadata synced")
}
