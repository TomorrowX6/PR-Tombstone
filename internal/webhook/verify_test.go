package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	secret := "TOKEN"
	body := []byte(`{"action":"closed"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifySignature(secret, body, signature) {
		t.Fatal("valid signature rejected")
	}
	if VerifySignature(secret, body, "sha256=00") {
		t.Fatal("invalid signature accepted")
	}
	if VerifySignature(secret, []byte("different"), signature) {
		t.Fatal("different body accepted")
	}
}
