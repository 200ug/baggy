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
// by walking the file tree to generate that data in the first place. If *remoteConn
// is not null, the metadata will be fetched from the remote server.
func NewMetadata(rootPath string, remoteConn *RemoteConn) (*Metadata, error) {
	var meta *Metadata

	if remoteConn != nil {
		// TODO: implement remote fetching here (simple call to transport.go via remoteConn)
		fmt.Println("[!] remote fetching not implemented yet")
	}

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
		files = append(files, Filedata{
			LocalPath: path,
			ContentHash: hash,
			ModifiedAt: info.ModTime().Unix(),
		})

		return nil
	})

	return files, err
}

