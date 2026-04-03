package internal

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	Version  int 	= 1
	Metafile string = ".meta." + FileExt
)

type Filedata struct {
	LocalPath	  string `json:"local_path"`
	ContentHash	  string `json:"content_hash"`
	ModifiedAt    int64  `json:"modified_at"`
}

type Metadata struct {
	Version int 		`json:"version"`
	Files   []Filedata  `json:"files"`
}

func (m *Metadata) WriteToFile(path string) (error) {
	js, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(path, js, 0644); err != nil {
		return err
	}

	return nil
}

// Creates a new Metadata object by either reading from an existing metafile or 
// by walking the file tree to generate that data in the first place.
func NewMetadata(rootPath string) (*Metadata, error) {
	var meta *Metadata

	files, err := walkDir(rootPath)
	if err != nil {
		return nil, err
	}
	meta = &Metadata{
		Version: Version,
		Files: files,
	}

	return meta, nil
}

type DiffResult struct {
	ToUpload   []Filedata
	ToDownload []Filedata
}

// Computes which files need to be uploaded or downloaded by walking the merged key set 
// of local and remote. LocalPath is relative to the sync root so keys match
// across machines. if remote is nil (first sync) all local files are queued for upload.
func Diff(local, remote *Metadata) DiffResult {
	if remote == nil {
		return DiffResult{ToUpload: local.Files}
	}

	index := func(files []Filedata) map[string]Filedata {
		m := make(map[string]Filedata, len(files))
		for _, f := range files {
			m[f.LocalPath] = f
		}
		return m
	}

	localMap  := index(local.Files)
	remoteMap := index(remote.Files)

	var result DiffResult

	for rel, lf := range localMap {
		rf, exists := remoteMap[rel]
		if !exists {
			result.ToUpload = append(result.ToUpload, lf)
		} else if lf.ContentHash != rf.ContentHash {
			if lf.ModifiedAt >= rf.ModifiedAt {
				result.ToUpload = append(result.ToUpload, lf)
			} else {
				result.ToDownload = append(result.ToDownload, rf)
			}
		}
	}

	for rel, rf := range remoteMap {
		if _, exists := localMap[rel]; !exists {
			result.ToDownload = append(result.ToDownload, rf)
		}
	}

	return result
}

// Returns files that are present in the old (local) metadata, but absent from the new (remote)
// one. In practice this means files that are deleted locally since the last sync. Returns nil
// if old is nil (i.e. no previous sync baseline).
func LocalDeletions(old, new *Metadata) []Filedata {
	if old == nil {
		return nil
	}

	newMap := make(map[string]struct{}, len(new.Files))
	for _, f := range new.Files {
		newMap[f.LocalPath] = struct{}{}
	}
	var deleted []Filedata
	for _, f := range old.Files {
		if _, exists := newMap[f.LocalPath]; !exists {
			deleted = append(deleted, f)
		}
	}

	return deleted
}

func excluded(name string) bool {
	for _, pattern := range FilenameExclusions {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

func walkDir(rootPath string) ([]Filedata, error) {
	files := make([]Filedata, 0)

	err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if excluded(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir // prevent WalkDir from going deeper into this dir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		
		info, err := d.Info()
		if err != nil {
			return err
		}
		hash, err := HashFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rootPath, path)
		if err != nil {
			return err
		}
		files = append(files, Filedata{
			LocalPath:   rel,
			ContentHash: hash,
			ModifiedAt:  info.ModTime().Unix(),
		})

		return nil
	})

	return files, err
}

