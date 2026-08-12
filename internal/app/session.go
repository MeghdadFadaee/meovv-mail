package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type TokenCipher struct{ aead cipher.AEAD }

func NewTokenCipher(key []byte) (TokenCipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return TokenCipher{}, err
	}
	aead, err := cipher.NewGCM(block)
	return TokenCipher{aead: aead}, err
}
func (c TokenCipher) Encrypt(value string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(value), nil), nil
}
func (c TokenCipher) Decrypt(value []byte) (string, error) {
	if len(value) < c.aead.NonceSize() {
		return "", fmt.Errorf("invalid encrypted token")
	}
	nonce, ciphertext := value[:c.aead.NonceSize()], value[c.aead.NonceSize():]
	raw, err := c.aead.Open(nil, nonce, ciphertext, nil)
	return string(raw), err
}
func RandomID(prefix string) string {
	raw := make([]byte, 18)
	_, _ = rand.Read(raw)
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}
