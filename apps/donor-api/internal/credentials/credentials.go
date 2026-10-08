// Package credentials preserves the legacy credential contract: the formats
// already stored in the deployed pre-rewrite database. Secrets and hashes must never be included in HTTP errors or application logs.
package credentials

import (
	"encoding/base64"
	"errors"
	"strconv"
	"unicode/utf8"

	"github.com/fernet/fernet-go"
)

var ErrInvalid = errors.New("invalid credential")

func key(value string) (*fernet.Key, error) {
	decoded, err := base64.URLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, ErrInvalid
	}
	k := new(fernet.Key)
	copy(k[:], decoded)
	return k, nil
}

// ValidateKey reports whether value is a usable encryption key, so services
// can refuse to start instead of failing on first use.
func ValidateKey(value string) error {
	_, err := key(value)
	return err
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
