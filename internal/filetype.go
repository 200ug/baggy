package internal

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var FilenameExclusions = []string{
	Metafile, // metafiles track the *local* state, thus shouldn't be mixed with remote's metafile
	".git",
	"node_modules",
	"*.tmp",
	"*.exe",
	"*.bin",
	"*.elf",
	"*.mp4",
	"*.mkv",
	"*.webm",
}

var BinarySignatures = [][]byte{
    {0x7F, 0x45, 0x4C, 0x46},                          // elf
    {0x4D, 0x5A},                                      // exe, dll
    {0xFE, 0xED, 0xFA, 0xCF},                          // mach-o 64-bit (be)
    {0xCF, 0xFA, 0xED, 0xFE},                          // mach-o 64-bit (le)
    {0xFE, 0xED, 0xFA, 0xCE},                          // mach-o 32-bit (be)
    {0xCE, 0xFA, 0xED, 0xFE},                          // mach-o 32-bit (le)
    {0xCA, 0xFE, 0xBA, 0xBE},                          // mach-o universal
    {0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00},  // wasm
    {0x64, 0x65, 0x78, 0x0A, 0x30, 0x33, 0x35, 0x00},  // android dalvik
}

var SkipBinaryFiles bool

const headerBufLen = 12

// Detects if a file should be filtered/excluded by (primarily) comparing its 
// name to a set of hardcoded patterns. Additionally if SkipBinaryFiles is 
// enabled, the file's header's magic bytes are compared against a set of
// known signatures to determine if the file is a binary.
func IsFileExcluded(path string) bool {
	filename := filepath.Base(path)
	for _, pattern := range FilenameExclusions {
		if ok, _ := filepath.Match(pattern, filename); ok {
			fmt.Printf("[dbg] excluding %s due to pattern\n", path)
			return true
		}
	}

	if SkipBinaryFiles {
		fmt.Printf("[dbg] checking magic bytes of %s\n", path)
		return matchesBinSignature(path)
	}

	return false
}

func matchesBinSignature(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	fileInfo, err := f.Stat()
	if err != nil {
		return false
	} else if fileInfo.IsDir() {
		return false
	}

	header := make([]byte, headerBufLen)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return false
	}
	header = header[:n]

	for _, sig := range BinarySignatures {
		if bytes.HasPrefix(header, sig) {
			fmt.Printf("[dbg] excluding %s due to signature\n", path)
			return true
		}
	}

	return false
}
