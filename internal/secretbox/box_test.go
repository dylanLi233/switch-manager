package secretbox

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestRoundTripUsesRandomNonce(t *testing.T) {
	key := bytes.Repeat([]byte{7}, KeyBytes)
	box, err := New(key, "v1")
	if err != nil {
		t.Fatal(err)
	}
	first, err := box.Encrypt([]byte("password"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := box.Encrypt([]byte("password"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("ciphertexts must differ")
	}
	plain, err := box.Decrypt(first)
	if err != nil || string(plain) != "password" {
		t.Fatalf("plain=%q err=%v", plain, err)
	}
}

func TestWrongVersionCannotDecrypt(t *testing.T) {
	key := bytes.Repeat([]byte{3}, KeyBytes)
	first, _ := New(key, "v1")
	second, _ := New(key, "v2")
	ciphertext, _ := first.Encrypt([]byte("secret"))
	if _, err := second.Decrypt(ciphertext); err == nil {
		t.Fatal("expected authentication failure")
	}
}

func TestNewBase64Requires32Bytes(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("short"))
	if _, err := NewBase64(encoded, "v1"); err == nil {
		t.Fatal("expected invalid key length")
	}
}
