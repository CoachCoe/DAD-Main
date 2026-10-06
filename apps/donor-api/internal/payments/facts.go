package payments

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

var ErrFacts = errors.New("invalid payment facts")

// maxUnix is 9999-12-31T23:59:59Z, the last second PostgreSQL timestamps and
// RFC 3339 can both represent.
const maxUnix = 253402300799

// Object is a processor object kept as raw JSON, so only the fields a caller
// verifies are ever decoded.
type Object map[string]json.RawMessage

func DecodeObject(data []byte) (Object, error) {
	var value Object
	if err := json.Unmarshal(data, &value); err != nil || value == nil {
		return nil, ErrFacts
	}
	return value, nil
}

// Absent reports whether key is missing or null.
func (o Object) Absent(key string) bool {
	raw := bytes.TrimSpace(o[key])
	return len(raw) == 0 || bytes.Equal(raw, []byte("null"))
}

func (o Object) Text(key string) string {
	var value string
	if json.Unmarshal(o[key], &value) != nil {
		return ""
	}
	return value
}

func (o Object) Int(key string) (int64, error) {
	var value int64
	if o.Absent(key) || json.Unmarshal(o[key], &value) != nil {
		return 0, ErrFacts
	}
	return value, nil
}

// IntDefault is Int, except that a missing key is zero.
func (o Object) IntDefault(key string) (int64, error) {
	if _, ok := o[key]; !ok {
		return 0, nil
	}
	return o.Int(key)
}

// Unix reads key as a Unix timestamp in seconds.
func (o Object) Unix(key string) (time.Time, error) {
	seconds, err := o.Int(key)
	if err != nil || seconds < 0 || seconds > maxUnix {
		return time.Time{}, ErrFacts
	}
	return time.Unix(seconds, 0).UTC(), nil
}

// BoolIs reports whether key is present as the JSON boolean want.
func (o Object) BoolIs(key string, want bool) bool {
	expected := "false"
	if want {
		expected = "true"
	}
	return string(bytes.TrimSpace(o[key])) == expected
}

func (o Object) Object(key string) (Object, error) { return DecodeObject(o[key]) }

func (o Object) Array(key string) ([]Object, error) {
	var value []Object
	if err := json.Unmarshal(o[key], &value); err != nil {
		return nil, ErrFacts
	}
	return value, nil
}
