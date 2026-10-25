// Package crypto encrypts the secrets stored in the application database,
// such as the passwords of the target database connections.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
)

// prefix marks the format of stored values, so a later change can tell
// them apart.
const prefix = "v1:"

// The secret key can be any text, a short passphrase included, so the AES
// key is derived with argon2id: guessing the secret from a leaked store then
// costs a full argon2 run per guess. The salt is fixed because there is one
// key per installation; it only has to differ from other uses of argon2.
var kdfSalt = []byte("dataport/secret-key")

const (
	kdfTime    = 3
	kdfMemory  = 64 * 1024
	kdfThreads = 4
)

type Cipher struct {
	aead cipher.AEAD
}

// New derives an AES-256-GCM cipher from the secret key, which can be any
// non-empty text.
func New(key string) (*Cipher, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("secret key is empty")
	}

	block, err := aes.NewCipher(argon2.IDKey([]byte(key), kdfSalt, kdfTime, kdfMemory, kdfThreads, 32))
	if err != nil {
		return nil, fmt.Errorf("new aes cipher; %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm; %w", err)
	}

	return &Cipher{aead: aead}, nil
}

// LoadOrCreateKey returns key when it is set, otherwise reads the key file in
// dir, creating it with a random key on first use.
func LoadOrCreateKey(key, dir string) (string, error) {
	if key != "" {
		return key, nil
	}

	path := filepath.Join(dir, "secret.key")

	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read secret key %s; %w", path, err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate secret key; %w", err)
	}

	generated := base64.RawStdEncoding.EncodeToString(raw)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir %s; %w", dir, err)
	}

	if err := os.WriteFile(path, []byte(generated+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write secret key %s; %w", path, err)
	}

	return generated, nil
}

func (c *Cipher) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce; %w", err)
	}

	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)

	return prefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) Decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}

	encoded, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return "", errors.New("unknown secret format")
	}

	data, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode secret; %w", err)
	}

	size := c.aead.NonceSize()
	if len(data) < size {
		return "", errors.New("secret is too short")
	}

	plain, err := c.aead.Open(nil, data[:size], data[size:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret; %w", err)
	}

	return string(plain), nil
}
