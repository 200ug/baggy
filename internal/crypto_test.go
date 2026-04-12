package internal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// testKeyHolder returns a KeyHolder with a fixed all-zero key for testing.
func testKeyHolder() *KeyHolder {
	return &KeyHolder{IDKey: make([]byte, 32)}
}

// encrypt / decrypt

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	kh := testKeyHolder()
	plaintext := []byte("the quick brown fox")

	ciphertext, err := kh.encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	got, err := kh.decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round-trip mismatch: got %q", got)
	}
}

func TestEncrypt_ProducesUniqueNonces(t *testing.T) {
	kh := testKeyHolder()
	a, _ := kh.encrypt([]byte("same"))
	b, _ := kh.encrypt([]byte("same"))
	if bytes.Equal(a, b) {
		t.Error("two encryptions of the same plaintext must not produce identical output")
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	kh := testKeyHolder()
	ct, _ := kh.encrypt([]byte("data"))
	ct[len(ct)-1] ^= 0xff // flip last byte (inside gcm tag)
	if _, err := kh.decrypt(ct); err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	kh := testKeyHolder()
	if _, err := kh.decrypt([]byte("short")); err == nil {
		t.Fatal("expected error for ciphertext shorter than nonce")
	}
}

// encryptfile / decryptfile

func TestEncryptFile_DecryptFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "plain.txt")
	enc := filepath.Join(dir, "plain.txt.wsftp")
	dst := filepath.Join(dir, "plain.dec.txt")

	want := []byte("file contents to protect")
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}

	kh := testKeyHolder()
	if err := kh.EncryptFile(src, enc); err != nil {
		t.Fatal(err)
	}
	if err := kh.DecryptFile(enc, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("round-trip mismatch: got %q", got)
	}
}

func TestDecryptFile_TamperedFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "plain.txt")
	enc := filepath.Join(dir, "plain.txt.wsftp")

	os.WriteFile(src, []byte("data"), 0o644)

	kh := testKeyHolder()
	if err := kh.EncryptFile(src, enc); err != nil {
		t.Fatal(err)
	}

	// flip a byte in the encrypted file
	raw, _ := os.ReadFile(enc)
	raw[len(raw)-1] ^= 0xff
	os.WriteFile(enc, raw, 0o600)

	if err := kh.DecryptFile(enc, filepath.Join(dir, "out.txt")); err == nil {
		t.Fatal("expected error for tampered encrypted file")
	}
}

func TestDecryptFile_AtomicWrite(t *testing.T) {
	// verify that decryptfile does not leave a .tmp file behind on success
	dir := t.TempDir()
	src := filepath.Join(dir, "f.txt")
	enc := filepath.Join(dir, "f.wsftp")
	dst := filepath.Join(dir, "out.txt")

	os.WriteFile(src, []byte("atomic"), 0o644)
	kh := testKeyHolder()
	kh.EncryptFile(src, enc)
	kh.DecryptFile(enc, dst)

	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file must not exist after successful DecryptFile")
	}
}
