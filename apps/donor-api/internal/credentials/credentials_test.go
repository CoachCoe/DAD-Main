package credentials

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestLegacyCredentials(t *testing.T) {
	var fixture struct {
		Key       string `json:"fernet_key"`
		Plaintext string `json:"fernet_plaintext"`
		Token     string `json:"legacy_fernet_token"`
		Checkout  []struct {
			Visitor  *string `json:"visitor"`
			Preimage string  `json:"preimage"`
			Hash     string  `json:"hash"`
		} `json:"checkout"`
	}
	data, err := os.ReadFile("../../testdata/credentials.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	plaintext, err := Decrypt([]byte(fixture.Token), fixture.Key)
	if err != nil || string(plaintext) != fixture.Plaintext {
		t.Fatalf("legacy Fernet token rejected: %v", err)
	}
	raw, err := base64.URLEncoding.DecodeString(fixture.Token)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	if _, err = Decrypt([]byte(base64.URLEncoding.EncodeToString(raw)), fixture.Key); !errors.Is(err, ErrInvalid) {
		t.Fatal("tampered token accepted")
	}
	wrongKey := base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	if _, err = Decrypt([]byte(fixture.Token), wrongKey); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong key accepted")
	}
	for _, input := range [][]byte{nil, {}, []byte("another public payload")} {
		token, err := Encrypt(input, fixture.Key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decrypt(token, fixture.Key)
		if err != nil || !bytes.Equal(got, input) {
			t.Fatalf("Fernet roundtrip failed: %v", err)
		}
	}
	if len(fixture.Checkout) != 4 {
		t.Fatal("missing checkout fixtures")
	}
	for _, tc := range fixture.Checkout {
		got, err := CheckoutPreimage("donor@example.org", 3000, "MONTHLY", tc.Visitor)
		digest := sha256.Sum256([]byte(got))
		if err != nil || got != tc.Preimage || hex.EncodeToString(digest[:]) != tc.Hash {
			t.Fatalf("legacy checkout hash mismatch: %v", err)
		}
	}
}

func TestValidateKeyRequiresThirtyTwoURLSafeBytes(t *testing.T) {
	valid := base64.URLEncoding.EncodeToString(make([]byte, 32))
	if err := ValidateKey(valid); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, invalid := range []string{"", "short", base64.URLEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\xff", 32)))} {
		if !errors.Is(ValidateKey(invalid), ErrInvalid) {
			t.Errorf("key %q accepted", invalid)
		}
	}
}
