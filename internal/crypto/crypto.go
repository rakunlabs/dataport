// Package crypto encrypts the secrets stored in the application database,
// such as the passwords of the target database connections.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

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

type sealer struct {
	aead cipher.AEAD
	// id is a digest of the derived key, to tell keys apart.
	id [32]byte
}

func newSealer(key string) (*sealer, error) {
	derived := argon2.IDKey([]byte(key), kdfSalt, kdfTime, kdfMemory, kdfThreads, 32)

	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher; %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm; %w", err)
	}

	return &sealer{aead: aead, id: sha256.Sum256(derived)}, nil
}

// Cipher encrypts with its current key. It still reads values sealed with
// the keys before, until they are sealed again.
type Cipher struct {
	mu       sync.RWMutex
	current  *sealer
	previous []*sealer
	// rotating holds encryption back while the stored secrets are sealed
	// again, so nothing is sealed with a key about to be replaced.
	rotating sync.RWMutex
}

// maxPrevious bounds the keys kept to read with.
const maxPrevious = 4

// New derives an AES-256-GCM cipher from the secret key, which can be any
// text. An empty key is used as it is: it still encrypts, but anyone with
// the store and dataport's source can read the passwords.
func New(key string) (*Cipher, error) {
	s, err := newSealer(key)
	if err != nil {
		return nil, err
	}

	return &Cipher{current: s}, nil
}

// Accept also reads values sealed with key, without encrypting with it.
func (c *Cipher) Accept(key string) error {
	s, err := newSealer(key)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.addPrevious(s)

	return nil
}

// addPrevious keeps a key to read with; the caller holds mu.
func (c *Cipher) addPrevious(s *sealer) {
	if c.current.id == s.id {
		return
	}

	for _, p := range c.previous {
		if p.id == s.id {
			return
		}
	}

	c.previous = append([]*sealer{s}, c.previous...)
	if len(c.previous) > maxPrevious {
		c.previous = c.previous[:maxPrevious]
	}
}

// Rotate makes key the current one. The key before stays readable.
func (c *Cipher) Rotate(key string) error {
	s, err := newSealer(key)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.current.id != s.id {
		old := c.current
		c.current = s
		c.previous = slices.DeleteFunc(c.previous, func(p *sealer) bool { return p.id == s.id })
		c.addPrevious(old)
	}

	return nil
}

// Rotating holds encryption back until done is called, for sealing the
// stored secrets again with a new key; Rotate is called before done.
func (c *Cipher) Rotating() (done func()) {
	c.rotating.Lock()

	return c.rotating.Unlock
}

// Is tells whether key is the current key.
func (c *Cipher) Is(key string) bool {
	s, err := newSealer(key)
	if err != nil {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	return subtle.ConstantTimeCompare(s.id[:], c.current.id[:]) == 1
}

func (c *Cipher) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}

	c.rotating.RLock()
	defer c.rotating.RUnlock()

	return c.encrypt(plain)
}

// encrypt seals with the current key, without waiting for a rotation.
func (c *Cipher) encrypt(plain string) (string, error) {
	c.mu.RLock()
	aead := c.current.aead
	c.mu.RUnlock()

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce; %w", err)
	}

	sealed := aead.Seal(nonce, nonce, []byte(plain), nil)

	return prefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a value sealed with the current key or the one before.
func (c *Cipher) Decrypt(value string) (string, error) {
	c.mu.RLock()
	current, previous := c.current, c.previous
	c.mu.RUnlock()

	plain, err := open(current, value)
	if err != nil {
		for _, p := range previous {
			if v, perr := open(p, value); perr == nil {
				return v, nil
			}
		}
	}

	return plain, err
}

// Current tells whether value was sealed with the current key.
func (c *Cipher) Current(value string) bool {
	c.mu.RLock()
	current := c.current
	c.mu.RUnlock()

	_, err := open(current, value)

	return err == nil
}

func open(s *sealer, value string) (string, error) {
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

	size := s.aead.NonceSize()
	if len(data) < size {
		return "", errors.New("secret is too short")
	}

	plain, err := s.aead.Open(nil, data[:size], data[size:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret; %w", err)
	}

	return string(plain), nil
}
