package internal

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
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

	localMetaPath := filepath.Join(rootPath, Metafile)
	if _, err := os.Stat(localMetaPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			files, err := walkDir(rootPath)
			if err != nil {
				return nil, err
			}
			meta = &Metadata{
				Version: Version,
				Files: files,
			}
		} else {
			return nil, err
		}
	} else {
		meta, err = metadataFromLocal(localMetaPath)
		if err != nil {
			return nil, err
		}
	}

	return meta, nil
}

func metadataFromLocal(path string) (*Metadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var meta Metadata
	parser := json.NewDecoder(file)
	if err = parser.Decode(&meta); err != nil {
		return nil, err
	}

	return &meta, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	hashBytes := hash.Sum(nil)

	return fmt.Sprintf("%x", hashBytes), nil
}

type DiffResult struct {
	ToUpload   []Filedata
	ToDownload []Filedata
}

// Computes which files need to be uploaded or downloaded by walking the merged key set 
// of local and remote. Relative paths (from rootPath) are used as the comparison key so 
// absolute paths on different machines don't matter. If remote is nil (first sync) all 
// local files are queued for upload.
func Diff(local, remote *Metadata, rootPath string) DiffResult {
	if remote == nil {
		return DiffResult{ToUpload: local.Files}
	}

	index := func(files []Filedata) map[string]Filedata {
		m := make(map[string]Filedata, len(files))
		for _, f := range files {
			rel, _ := filepath.Rel(rootPath, f.LocalPath)
			m[rel] = f
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

func excluded(name string) bool {
	for _, pattern := range Exclusions {
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
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		files = append(files, Filedata{
			LocalPath: absPath,
			ContentHash: hash,
			ModifiedAt: info.ModTime().Unix(),
		})

		return nil
	})

	return files, err
}

