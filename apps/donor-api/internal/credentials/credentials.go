// Package credentials preserves the legacy credential contract: the formats
// already stored in the deployed pre-rewrite database. Secrets and hashes must never be included in HTTP errors or application logs.
package credentials

import (
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/alexedwards/argon2id"
	"github.com/fernet/fernet-go"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
)

var ErrInvalid = errors.New("invalid credential")

// The legacy contract's Argon2id writer parameters, deliberately independent of CPU count.
func HashPassword(password string) (string, error) {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 128 {
		return "", ErrInvalid
	}
	return argon2id.CreateHash(password, &argon2id.Params{Memory: 65536, Iterations: 3, Parallelism: 4, SaltLength: 16, KeyLength: 32})
}
func VerifyPassword(password, encoded string) (bool, error) {
	if len(encoded) > 512 || !utf8.ValidString(password) || utf8.RuneCountInString(password) > 128 {
		return false, ErrInvalid
	}
	params, salt, key, err := argon2id.DecodeHash(encoded)
	if err != nil {
		return false, ErrInvalid
	}
	// Reject malformed/extreme stored parameters before allocating attacker-sized memory.
	if params.Memory < 8*uint32(params.Parallelism) || params.Memory > 262144 || params.Iterations == 0 || params.Iterations > 10 ||
		params.Parallelism == 0 || params.Parallelism > 16 || len(salt) < 8 || len(salt) > 64 || len(key) < 16 || len(key) > 64 {
		return false, ErrInvalid
	}
	ok, err := argon2id.ComparePasswordAndHash(password, encoded)
	if err != nil {
		return false, ErrInvalid
	}
	return ok, nil
}
func key(value string) (*fernet.Key, error) {
	decoded, err := base64.URLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, ErrInvalid
	}
	k := new(fernet.Key)
	copy(k[:], decoded)
	return k, nil
}
func Encrypt(value []byte, encodedKey string) ([]byte, error) {
	k, err := key(encodedKey)
	if err != nil {
		return nil, err
	}
	return fernet.EncryptAndSign(value, k)
}
func Decrypt(token []byte, encodedKey string) ([]byte, error) {
	k, err := key(encodedKey)
	if err != nil {
		return nil, err
	}
	if _, err = base64.URLEncoding.Strict().DecodeString(string(token)); err != nil {
		return nil, ErrInvalid
	}
	// Expiration comes from login/session records, not the Fernet creation timestamp.
	result := fernet.VerifyAndDecrypt(token, 0, []*fernet.Key{k})
	if result == nil {
		return nil, ErrInvalid
	}
	return result, nil
}
func VerifyOTP(secret []byte, code string, now time.Time, previous int64) (int64, error) {
	if len(secret) != 20 || len(code) != 6 {
		return 0, ErrInvalid
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, ErrInvalid
		}
	}
	step := now.Unix() / 30
	for _, counter := range []int64{step, step - 1, step + 1} {
		if counter <= previous || counter < 0 {
			continue
		}
		matched, err := hotp.ValidateCustom(code, uint64(counter), base32.StdEncoding.EncodeToString(secret), hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			return 0, ErrInvalid
		}
		if matched {
			return counter, nil
		}
	}
	return 0, ErrInvalid
}

// QuoteASCII reproduces the legacy contract's ASCII-only JSON string escaping.
// Checkout request hashes must match those recorded by the pre-rewrite service.
func QuoteASCII(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalid
	}
	output := []byte{'"'}
	appendHex := func(r rune) {
		const digits = "0123456789abcdef"
		output = append(output, '\\', 'u', digits[(r>>12)&15], digits[(r>>8)&15], digits[(r>>4)&15], digits[r&15])
	}
	for _, r := range value {
		switch r {
		case '"', '\\':
			output = append(output, '\\', byte(r))
		case '\b':
			output = append(output, '\\', 'b')
		case '\f':
			output = append(output, '\\', 'f')
		case '\n':
			output = append(output, '\\', 'n')
		case '\r':
			output = append(output, '\\', 'r')
		case '\t':
			output = append(output, '\\', 't')
		default:
			if r < 32 || r >= 127 {
				if r > 0xffff {
					r -= 0x10000
					appendHex(0xd800 + (r >> 10))
					appendHex(0xdc00 + (r & 1023))
				} else {
					appendHex(r)
				}
			} else {
				output = append(output, byte(r))
			}
		}
	}
	output = append(output, '"')
	return string(output), nil
}

// CheckoutPreimage expects the canonical email produced by the identity adapter.
// Nil visitor and empty visitor are distinct in the existing request hash contract.
func CheckoutPreimage(email string, amount int64, frequency string, visitor *string) (string, error) {
	if amount < 100 || (frequency != "MONTHLY" && frequency != "ONE_TIME") {
		return "", ErrInvalid
	}
	quoted, err := QuoteASCII(email)
	if err != nil {
		return "", err
	}
	cookie := "null"
	if visitor != nil {
		cookie, err = QuoteASCII(*visitor)
		if err != nil {
			return "", err
		}
	}
	return "[" + quoted + "," + strconv.FormatInt(amount, 10) + ",\"" + frequency + "\"," + cookie + "]", nil
}
