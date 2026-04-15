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

// verification file tests

func TestCreateVerificationFile_Success(t *testing.T) {
	dir := t.TempDir()
	kh := testKeyHolder()

	err := kh.createVerificationFile(dir)
	if err != nil {
		t.Fatalf("createVerificationFile failed: %v", err)
	}

	// verify file exists
	verifyPath := filepath.Join(dir, VerificationFile)
	if _, err := os.Stat(verifyPath); os.IsNotExist(err) {
		t.Error("verification file was not created")
	}

	// verify it can be decrypted and matches KnownPlaintext
	ciphertext, err := os.ReadFile(verifyPath)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := kh.decrypt(ciphertext)
	if err != nil {
		t.Fatalf("failed to decrypt verification file: %v", err)
	}
	if string(plaintext) != KnownPlaintext {
		t.Errorf("decrypted content mismatch: got %q, want %q", plaintext, KnownPlaintext)
	}
}

func TestVerifyKey_ValidKey(t *testing.T) {
	dir := t.TempDir()
	kh := testKeyHolder()

	// create verification file
	if err := kh.createVerificationFile(dir); err != nil {
		t.Fatal(err)
	}

	// verify the same key works
	valid, err := kh.verifyKey(dir)
	if err != nil {
		t.Fatalf("verifyKey failed: %v", err)
	}
	if !valid {
		t.Error("expected valid key to succeed verification")
	}
}

func TestVerifyKey_InvalidKey(t *testing.T) {
	dir := t.TempDir()
	keyA := make([]byte, 32) // all zeros
	keyB := make([]byte, 32)
	keyB[0] = 0x01 // different key

	khA := &KeyHolder{IDKey: keyA}
	khB := &KeyHolder{IDKey: keyB}

	// create verification file with keyA
	if err := khA.createVerificationFile(dir); err != nil {
		t.Fatal(err)
	}

	// verify keyB fails
	valid, err := khB.verifyKey(dir)
	if err != nil {
		t.Fatalf("verifyKey failed: %v", err)
	}
	if valid {
		t.Error("expected invalid key to fail verification")
	}
}

func TestVerifyKey_MissingFile(t *testing.T) {
	dir := t.TempDir()
	kh := testKeyHolder()

	// verify without creating file first
	valid, err := kh.verifyKey(dir)
	if err != nil {
		t.Fatalf("verifyKey failed: %v", err)
	}
	if valid {
		t.Error("expected missing file to return false")
	}
}

func TestVerifyKey_TamperedFile(t *testing.T) {
	dir := t.TempDir()
	kh := testKeyHolder()

	// create verification file
	if err := kh.createVerificationFile(dir); err != nil {
		t.Fatal(err)
	}

	// tamper with the file
	verifyPath := filepath.Join(dir, VerificationFile)
	ciphertext, _ := os.ReadFile(verifyPath)
	ciphertext[len(ciphertext)-1] ^= 0xff
	os.WriteFile(verifyPath, ciphertext, 0o600)

	// verify tampered file fails
	valid, err := kh.verifyKey(dir)
	if err != nil {
		t.Fatalf("verifyKey failed: %v", err)
	}
	if valid {
		t.Error("expected tampered file to fail verification")
	}
}

func TestVerifyKey_WrongPlaintext(t *testing.T) {
	dir := t.TempDir()
	kh := testKeyHolder()

	// create verification file with wrong plaintext
	wrongPlaintext := []byte("wrong plaintext content")
	ciphertext, err := kh.encrypt(wrongPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	verifyPath := filepath.Join(dir, VerificationFile)
	if err := os.WriteFile(verifyPath, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}

	// verify wrong plaintext fails
	valid, err := kh.verifyKey(dir)
	if err != nil {
		t.Fatalf("verifyKey failed: %v", err)
	}
	if valid {
		t.Error("expected wrong plaintext to fail verification")
	}
}
