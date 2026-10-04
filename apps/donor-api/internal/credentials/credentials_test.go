package credentials

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPythonCredentials(t *testing.T) {
	var fixture struct {
		Key       string `json:"fernet_key"`
		Plaintext string `json:"fernet_plaintext"`
		Token     string `json:"python_fernet_token"`
		Password  string `json:"password"`
		Hash      string `json:"python_argon2id"`
		Secret    string `json:"otp_secret"`
		Time      int64  `json:"otp_time"`
		Code      string `json:"otp_code"`
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
	ok, err := VerifyPassword(fixture.Password, fixture.Hash)
	if err != nil || !ok {
		t.Fatalf("Python password rejected: %v", err)
	}
	ok, err = VerifyPassword("wrong", fixture.Hash)
	if err != nil || ok {
		t.Fatal("wrong password accepted")
	}
	plaintext, err := Decrypt([]byte(fixture.Token), fixture.Key)
	if err != nil || string(plaintext) != fixture.Plaintext {
		t.Fatalf("Python Fernet rejected: %v", err)
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
	secret, err := base32.StdEncoding.DecodeString(fixture.Secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int64{-30, 0, 30} {
		counter, err := VerifyOTP(secret, fixture.Code, time.Unix(fixture.Time+offset, 0), -1)
		if err != nil || counter != fixture.Time/30 {
			t.Fatalf("Python OTP window failed: %d %v", offset, err)
		}
	}
	for _, tc := range []struct {
		now, previous int64
		code          string
	}{
		{fixture.Time, fixture.Time / 30, fixture.Code}, {fixture.Time + 60, -1, fixture.Code},
		{fixture.Time, -1, "00000"}, {fixture.Time, -1, "abcdef"},
	} {
		if _, err = VerifyOTP(secret, tc.code, time.Unix(tc.now, 0), tc.previous); !errors.Is(err, ErrInvalid) {
			t.Fatal("expired/replayed/malformed OTP accepted")
		}
	}
	if len(fixture.Checkout) != 4 {
		t.Fatal("missing checkout fixtures")
	}
	for _, tc := range fixture.Checkout {
		got, err := CheckoutPreimage("donor@example.org", 3000, "MONTHLY", tc.Visitor)
		digest := sha256.Sum256([]byte(got))
		if err != nil || got != tc.Preimage || hex.EncodeToString(digest[:]) != tc.Hash {
			t.Fatalf("Python checkout hash mismatch: %v", err)
		}
	}
}

func TestPasswordBounds(t *testing.T) {
	for _, password := range []string{"short", strings.Repeat("é", 129), string([]byte{255})} {
		if _, err := HashPassword(password); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid password accepted")
		}
	}
	encoded, err := HashPassword(strings.Repeat("é", 12))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatal("writer parameters changed")
	}
	if ok, err := VerifyPassword(strings.Repeat("é", 12), encoded); err != nil || !ok {
		t.Fatal("unicode password rejected")
	}
	for _, hash := range []string{"", strings.Repeat("x", 513), strings.Replace(encoded, "m=65536", "m=4294967295", 1), strings.Replace(encoded, "p=4", "p=0", 1), strings.Replace(encoded, "t=3", "t=0", 1)} {
		if _, err := VerifyPassword("password", hash); !errors.Is(err, ErrInvalid) {
			t.Fatal("malformed hash accepted")
		}
	}
}
