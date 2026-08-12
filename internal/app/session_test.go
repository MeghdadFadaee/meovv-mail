package app

import (
	"bytes"
	"testing"
)

func TestTokenCipherRoundTripAndRandomNonce(t *testing.T) {
	cipher, err := NewTokenCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	one, err := cipher.Encrypt("access-token")
	if err != nil {
		t.Fatal(err)
	}
	two, _ := cipher.Encrypt("access-token")
	if bytes.Equal(one, two) {
		t.Fatal("ciphertext reused a nonce")
	}
	plain, err := cipher.Decrypt(one)
	if err != nil || plain != "access-token" {
		t.Fatalf("round trip = %q, %v", plain, err)
	}
}
