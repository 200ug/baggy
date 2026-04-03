package internal

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func knownHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:])
}

func tmpFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// hashfile

func TestHashFile_KnownContent(t *testing.T) {
	p := tmpFile(t, t.TempDir(), "f.txt", "hello")
	got, err := HashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != knownHash("hello") {
		t.Errorf("got %s, want %s", got, knownHash("hello"))
	}
}

func TestHashFile_Missing(t *testing.T) {
	if _, err := HashFile("/no/such/file"); err == nil {
		t.Fatal("expected error")
	}
}

// excluded

func TestExcluded_ExactMatch(t *testing.T) {
	orig := FilenameExclusions
	FilenameExclusions = []string{".git", "node_modules"}
	t.Cleanup(func() { FilenameExclusions = orig })

	if !excluded(".git") {
		t.Error("expected .git to be excluded")
	}
}

func TestExcluded_GlobMatch(t *testing.T) {
	orig := FilenameExclusions
	FilenameExclusions = []string{"*.tmp"}
	t.Cleanup(func() { FilenameExclusions = orig })

	if !excluded("foo.tmp") {
		t.Error("expected foo.tmp to match *.tmp")
	}
}

func TestExcluded_NoMatch(t *testing.T) {
	orig := FilenameExclusions
	FilenameExclusions = []string{".git", "*.tmp"}
	t.Cleanup(func() { FilenameExclusions = orig })

	if excluded("notes.txt") {
		t.Error("notes.txt should not be excluded")
	}
}

// walkdir

func TestWalkDir_ReturnsFiles(t *testing.T) {
	dir := t.TempDir()
	tmpFile(t, dir, "a.txt", "aaa")
	tmpFile(t, dir, "b.txt", "bbb")

	files, err := walkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("got %d files, want 2", len(files))
	}
}

func TestWalkDir_ExcludesMetafile(t *testing.T) {
	dir := t.TempDir()
	tmpFile(t, dir, "real.txt", "data")
	tmpFile(t, dir, Metafile, `{"version":1,"files":[]}`)

	files, err := walkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if filepath.Base(f.LocalPath) == Metafile {
			t.Error("metafile must not appear in walk results")
		}
	}
	if len(files) != 1 {
		t.Errorf("got %d files, want 1", len(files))
	}
}

func TestWalkDir_HashCorrect(t *testing.T) {
	dir := t.TempDir()
	tmpFile(t, dir, "c.txt", "content")

	files, err := walkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].ContentHash != knownHash("content") {
		t.Errorf("wrong hash: %s", files[0].ContentHash)
	}
}

func TestWalkDir_ExcludesMatchingFile(t *testing.T) {
	orig := FilenameExclusions
	FilenameExclusions = []string{"*.tmp"}
	t.Cleanup(func() { FilenameExclusions = orig })

	dir := t.TempDir()
	tmpFile(t, dir, "keep.txt", "data")
	tmpFile(t, dir, "discard.tmp", "data")

	files, err := walkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0].LocalPath) != "keep.txt" {
		t.Errorf("unexpected files: %+v", files)
	}
}

func TestWalkDir_ExcludesMatchingDir_SkipsSubtree(t *testing.T) {
	orig := FilenameExclusions
	FilenameExclusions = []string{"node_modules"}
	t.Cleanup(func() { FilenameExclusions = orig })

	dir := t.TempDir()
	tmpFile(t, dir, "keep.txt", "data")
	sub := filepath.Join(dir, "node_modules")
	os.Mkdir(sub, 0o755)
	tmpFile(t, sub, "inside.js", "data")

	files, err := walkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0].LocalPath) != "keep.txt" {
		t.Errorf("unexpected files: %+v", files)
	}
}

func TestWalkDir_Empty(t *testing.T) {
	files, err := walkDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want 0", len(files))
	}
}

// newmetadata

func TestNewMetadata_NoMetafile_Walks(t *testing.T) {
	dir := t.TempDir()
	tmpFile(t, dir, "x.txt", "x")

	m, err := NewMetadata(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != Version || len(m.Files) != 1 {
		t.Errorf("unexpected metadata: %+v", m)
	}
}

func TestNewMetadata_StatError_PropagatesError(t *testing.T) {
	dir := t.TempDir()
	// make the directory unreadable so stat on the metafile path fails with a non ErrNotExist error
	restricted := filepath.Join(dir, "r")
	if err := os.Mkdir(restricted, 0o000); err != nil {
		t.Skip("cannot create restricted dir")
	}
	t.Cleanup(func() { os.Chmod(restricted, 0o755) })

	if _, err := NewMetadata(restricted); err == nil {
		t.Fatal("expected error for unreadable directory")
	}
}

// diff

func fd(name, hash string, mtime int64) Filedata {
	return Filedata{LocalPath: name, ContentHash: hash, ModifiedAt: mtime}
}

func TestDiff_NilRemote_AllUploaded(t *testing.T) {
	local := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1), fd("b.txt", "h2", 2)}}

	d := Diff(local, nil)
	if len(d.ToUpload) != 2 || len(d.ToDownload) != 0 {
		t.Errorf("got upload=%d download=%d, want 2/0", len(d.ToUpload), len(d.ToDownload))
	}
}

func TestDiff_LocalOnly_Uploads(t *testing.T) {
	local  := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1)}}
	remote := &Metadata{Files: []Filedata{}}

	d := Diff(local, remote)
	if len(d.ToUpload) != 1 || len(d.ToDownload) != 0 {
		t.Errorf("got upload=%d download=%d, want 1/0", len(d.ToUpload), len(d.ToDownload))
	}
}

func TestDiff_RemoteOnly_Downloads(t *testing.T) {
	local  := &Metadata{Files: []Filedata{}}
	remote := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1)}}

	d := Diff(local, remote)
	if len(d.ToUpload) != 0 || len(d.ToDownload) != 1 {
		t.Errorf("got upload=%d download=%d, want 0/1", len(d.ToUpload), len(d.ToDownload))
	}
}

func TestDiff_InSync_NoAction(t *testing.T) {
	f := fd("a.txt", "samehash", 1)
	local  := &Metadata{Files: []Filedata{f}}
	remote := &Metadata{Files: []Filedata{f}}

	d := Diff(local, remote)
	if len(d.ToUpload) != 0 || len(d.ToDownload) != 0 {
		t.Errorf("got upload=%d download=%d, want 0/0", len(d.ToUpload), len(d.ToDownload))
	}
}

func TestDiff_LocalNewer_Uploads(t *testing.T) {
	local  := &Metadata{Files: []Filedata{fd("a.txt", "new", 10)}}
	remote := &Metadata{Files: []Filedata{fd("a.txt", "old", 5)}}

	d := Diff(local, remote)
	if len(d.ToUpload) != 1 || len(d.ToDownload) != 0 {
		t.Errorf("got upload=%d download=%d, want 1/0", len(d.ToUpload), len(d.ToDownload))
	}
}

func TestDiff_RemoteNewer_Downloads(t *testing.T) {
	local  := &Metadata{Files: []Filedata{fd("a.txt", "old", 5)}}
	remote := &Metadata{Files: []Filedata{fd("a.txt", "new", 10)}}

	d := Diff(local, remote)
	if len(d.ToUpload) != 0 || len(d.ToDownload) != 1 {
		t.Errorf("got upload=%d download=%d, want 0/1", len(d.ToUpload), len(d.ToDownload))
	}
}

func TestDiff_BothEmpty(t *testing.T) {
	d := Diff(&Metadata{}, &Metadata{})
	if len(d.ToUpload) != 0 || len(d.ToDownload) != 0 {
		t.Errorf("got upload=%d download=%d, want 0/0", len(d.ToUpload), len(d.ToDownload))
	}
}

// localdeletions

func TestLocalDeletions_NilOld_ReturnsNil(t *testing.T) {
	new := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1)}}
	if got := LocalDeletions(nil, new); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestLocalDeletions_NoDeletions_ReturnsEmpty(t *testing.T) {
	m := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1), fd("b.txt", "h2", 2)}}
	if got := LocalDeletions(m, m); len(got) != 0 {
		t.Errorf("expected no deletions, got %+v", got)
	}
}

func TestLocalDeletions_OneDeleted(t *testing.T) {
	old := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1), fd("b.txt", "h2", 2)}}
	new := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1)}}

	got := LocalDeletions(old, new)
	if len(got) != 1 || got[0].LocalPath != "b.txt" {
		t.Errorf("expected b.txt deleted, got %+v", got)
	}
}

func TestLocalDeletions_AllDeleted(t *testing.T) {
	old := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1), fd("b.txt", "h2", 2)}}
	new := &Metadata{Files: []Filedata{}}

	got := LocalDeletions(old, new)
	if len(got) != 2 {
		t.Errorf("expected 2 deletions, got %d", len(got))
	}
}

func TestLocalDeletions_NewFileNotFlaggedAsDeleted(t *testing.T) {
	old := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1)}}
	new := &Metadata{Files: []Filedata{fd("a.txt", "h1", 1), fd("b.txt", "h2", 2)}}

	got := LocalDeletions(old, new)
	if len(got) != 0 {
		t.Errorf("expected no deletions, got %+v", got)
	}
}

func TestLocalDeletions_BothEmpty_ReturnsEmpty(t *testing.T) {
	got := LocalDeletions(&Metadata{}, &Metadata{})
	if len(got) != 0 {
		t.Errorf("expected no deletions, got %+v", got)
	}
}

// writetofile

func TestWriteToFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := &Metadata{Version: Version, Files: []Filedata{{LocalPath: "/z", ContentHash: "zz", ModifiedAt: 42}}}
	p := filepath.Join(dir, Metafile)

	if err := m.WriteToFile(p); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var got Metadata
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != m.Version || len(got.Files) != 1 || got.Files[0].LocalPath != "/z" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}
