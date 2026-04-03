package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func tmpBytesFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIsFileExcluded_ELF(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "mybinary.elf", []byte{0x7F, 0x45, 0x4C, 0x46, 0x02, 0x01, 0x01})

	if !IsFileExcluded(path) {
		t.Error("expected ELF to be detected as excluded binary")
	}
}

func TestIsFileExcluded_EXE(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "program.exe", []byte{0x4D, 0x5A})

	if !IsFileExcluded(path) {
		t.Error("expected EXE to be detected as excluded binary")
	}
}

func TestIsFileExcluded_MachO64(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "binary", []byte{0xFE, 0xED, 0xFA, 0xCF, 0x00, 0x00})

	if !IsFileExcluded(path) {
		t.Error("expected mach-o 64-bit to be detected as excluded binary")
	}
}

func TestIsFileExcluded_MachOUniversal(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "binary", []byte{0xCA, 0xFE, 0xBA, 0xBE, 0x00, 0x00})

	if !IsFileExcluded(path) {
		t.Error("expected mach-o universal to be detected as excluded binary")
	}
}

func TestIsFileExcluded_WASM(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "module.wasm", []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00})

	if !IsFileExcluded(path) {
		t.Error("expected WASM to be detected as excluded binary")
	}
}

func TestIsFileExcluded_Dalvik(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "classes.dex", []byte{0x64, 0x65, 0x78, 0x0A, 0x30, 0x33, 0x35, 0x00})

	if !IsFileExcluded(path) {
		t.Error("expected Dalvik to be detected as excluded binary")
	}
}

func TestIsFileExcluded_PlainText(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "readme.txt", []byte("Hello, World! This is plain text."))

	if IsFileExcluded(path) {
		t.Error("expected plain text to NOT be detected as excluded binary")
	}
}

func TestIsFileExcluded_SourceCode(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	dir := t.TempDir()
	code := "package main\n\nfunc main() {\n    println(\"Hello\")\n}"
	path := tmpBytesFile(t, dir, "main.go", []byte(code))

	if IsFileExcluded(path) {
		t.Error("expected source code to NOT be detected as excluded binary")
	}
}

func TestIsFileExcluded_BinaryDetectionDisabled(t *testing.T) {
	orig := SkipBinaryFiles
	origExcl := FilenameExclusions
	SkipBinaryFiles = false
	FilenameExclusions = []string{}
	t.Cleanup(func() {
		SkipBinaryFiles = orig
		FilenameExclusions = origExcl
	})

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "mybinary.xyz", []byte{0x7F, 0x45, 0x4C, 0x46, 0x02, 0x01, 0x01})

	if IsFileExcluded(path) {
		t.Error("expected ELF to NOT be excluded when SkipBinaryFiles is false")
	}
}

func TestIsFileExcluded_DirectoryNotTreatedAsBinary(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	dir := t.TempDir()
	dirPath := filepath.Join(dir, "mydir")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if IsFileExcluded(dirPath) {
		t.Error("expected directory to NOT be detected as excluded binary")
	}
}

func TestIsFileExcluded_MissingFile(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	if IsFileExcluded("/no/such/file") {
		t.Error("missing file should not be detected as excluded")
	}
}

func TestIsFileExcluded_UnknownBinarySignature(t *testing.T) {
	orig := SkipBinaryFiles
	origExcl := FilenameExclusions
	SkipBinaryFiles = true
	FilenameExclusions = []string{}
	t.Cleanup(func() {
		SkipBinaryFiles = orig
		FilenameExclusions = origExcl
	})

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "unknown.xyz", []byte{0xFF, 0xFF, 0xFF, 0xFF})

	if IsFileExcluded(path) {
		t.Error("expected unknown binary signature to NOT be excluded")
	}
}

func TestIsFileExcluded_FilenamePatternOverridesBinaryCheck(t *testing.T) {
	orig := SkipBinaryFiles
	SkipBinaryFiles = true
	t.Cleanup(func() { SkipBinaryFiles = orig })

	origExcl := FilenameExclusions
	FilenameExclusions = []string{"*.mp4"}
	t.Cleanup(func() { FilenameExclusions = origExcl })

	dir := t.TempDir()
	path := tmpBytesFile(t, dir, "video.mp4", []byte("not really mp4"))

	if !IsFileExcluded(path) {
		t.Error("expected filename pattern match to exclude file regardless of binary content")
	}
}

func TestIsFileExcluded_DirectoryByName(t *testing.T) {
	origExcl := FilenameExclusions
	FilenameExclusions = []string{"node_modules"}
	t.Cleanup(func() { FilenameExclusions = origExcl })

	dir := t.TempDir()
	dirPath := filepath.Join(dir, "node_modules")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if !IsFileExcluded(dirPath) {
		t.Error("expected directory named 'node_modules' to be excluded")
	}
}

func TestWalkDir_SkipsBinaryFilesWhenEnabled(t *testing.T) {
	origSkip := SkipBinaryFiles
	origBinaryExcl := FilenameExclusions
	SkipBinaryFiles = true
	FilenameExclusions = []string{}
	t.Cleanup(func() {
		SkipBinaryFiles = origSkip
		FilenameExclusions = origBinaryExcl
	})

	dir := t.TempDir()
	tmpBytesFile(t, dir, "readme.txt", []byte("plain text"))
	tmpBytesFile(t, dir, "mybinary.elf", []byte{0x7F, 0x45, 0x4C, 0x46, 0x02, 0x01, 0x01})

	files, err := walkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Errorf("got %d files, want 1", len(files))
	}
	if files[0].LocalPath != "readme.txt" {
		t.Errorf("got %s, want readme.txt", files[0].LocalPath)
	}
}

func TestWalkDir_IncludesBinaryFilesWhenDisabled(t *testing.T) {
	origSkip := SkipBinaryFiles
	origExcl := FilenameExclusions
	SkipBinaryFiles = false
	FilenameExclusions = []string{}
	t.Cleanup(func() {
		SkipBinaryFiles = origSkip
		FilenameExclusions = origExcl
	})

	dir := t.TempDir()
	tmpBytesFile(t, dir, "readme.txt", []byte("plain text"))
	tmpBytesFile(t, dir, "mybinary.xyz", []byte{0x7F, 0x45, 0x4C, 0x46, 0x02, 0x01, 0x01})

	files, err := walkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("got %d files, want 2", len(files))
	}
}
