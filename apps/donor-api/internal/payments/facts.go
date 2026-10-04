package payments

import (
	"bytes"
	"encoding/json"
	"errors"
)

var ErrFacts = errors.New("invalid payment facts")

type Object map[string]json.RawMessage

func DecodeObject(data []byte) (Object, error) {
	var value Object
	if err := json.Unmarshal(data, &value); err != nil || value == nil {
		return nil, ErrFacts
	}
	return value, nil
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
	raw := o[key]
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return 0, ErrFacts
	}
	return value, nil
}
func (o Object) IntDefault(key string) (int64, error) {
	if _, ok := o[key]; !ok {
		return 0, nil
	}
	return o.Int(key)
}
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
