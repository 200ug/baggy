package internal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/crypto/argon2"
	"golang.org/x/term"
)

const (
	FileExt             string = "wsftp"
	VerificationFile    string = ".verysecure." + FileExt
	KnownPlaintext      string = "Ancient Astronauts of Agartha"
	MaxPasswordAttempts int    = 3
)

var ErrOutOfAttempts error = errors.New("out of password attempts")

type KeyHolder struct {
	IDKey []byte
}

// Takes user input as password from the command line, derives a key from it
// (combined with salt and cost parameters as per the Argon2id library), and
// attempts to verify this key against a verification file (ensures consistent
// passwords) unless this is the initial sync, in which case it creates this
// verification file.
//
// Notably all passwords are trimmed, i.e. whitespace won't persist.
func NewKeyHolder(salt []byte, rootPath string, isFirstSync bool) (*KeyHolder, error) {
	for range MaxPasswordAttempts {
		fmt.Printf("[?] encryption password: ")
		bytePassword, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			return nil, err
		}

		password := strings.TrimSpace(string(bytePassword))
		key := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32) // rfc 9106 section 7.3
		kh := &KeyHolder{IDKey: key}

		if isFirstSync {
			err = kh.createVerificationFile(rootPath)
			return kh, err
		}
		isValidKey, err := kh.verifyKey(rootPath)
		if err != nil {
			return nil, err
		} else if isValidKey {
			return kh, nil
		} else {
			fmt.Println("[!] given key was invalid, please try again")
		}
	}

	return nil, ErrOutOfAttempts
}

// Attempt to decrypt the VerificationFile. If the process succeeds and the
// contents match to KnownPlaintext, we know the key is the same as with every
// previous sync round.
func (kh *KeyHolder) verifyKey(rootPath string) (bool, error) {
	ffp := filepath.Join(rootPath, VerificationFile)
	if _, err := os.Stat(ffp); os.IsNotExist(err) {
		// shouldn't happen, as either init or fetch from remote should've been done beforehand
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to stat verification file: %w", err)
	}

	ciphertext, err := os.ReadFile(ffp)
	if err != nil {
		return false, fmt.Errorf("failed to read verification file: %w", err)
	}
	plaintext, err := kh.decrypt(ciphertext)
	if err != nil {
		return false, nil
	}

	return string(plaintext) == KnownPlaintext, nil
}

// Encrypts KnownPlaintext with the assigned key and writes it to the specified
// root directory. Used during *first* sync (i.e. no remote state yet).
func (kh *KeyHolder) createVerificationFile(rootPath string) error {
	ffp := filepath.Join(rootPath, VerificationFile)
	ciphertext, err := kh.encrypt([]byte(KnownPlaintext))
	if err != nil {
		return fmt.Errorf("failed to encrypt verification data: %w", err)
	}
	if err := os.WriteFile(ffp, ciphertext, 0o600); err != nil {
		return fmt.Errorf("failed to write verification file: %w", err)
	}

	return nil
}

func (kh *KeyHolder) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(kh.IDKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (kh *KeyHolder) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(kh.IDKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	return gcm.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
}

// Reads source from disk, encrypts it with AES-256-GCM, and writes the result to destination.
func (kh *KeyHolder) EncryptFile(src, dst string) error {
	plaintext, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	ciphertext, err := kh.encrypt(plaintext)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, ciphertext, 0o600)
}

// Reads source from disk, decrypts it with AES-256-GCM, and writes the result to destination
// atomically (by renaming a temporary file).
func (kh *KeyHolder) DecryptFile(src, dst string) error {
	ciphertext, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	plaintext, err := kh.decrypt(ciphertext)
	if err != nil {
		return err
	}

	tmp := dst + ".tmp"
	if err = os.WriteFile(tmp, plaintext, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func HashFile(path string) (string, error) {
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
