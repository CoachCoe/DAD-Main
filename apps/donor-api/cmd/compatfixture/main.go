// compatfixture emits PUBLIC TEST VALUES to verify Python can read Go-written
// credentials. It accepts no deployment configuration or user input.
package main

import (
	"encoding/base64"
	"encoding/json"
	"os"

	"impactgraph/donor-api/internal/credentials"
)

func main() {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	encoded := base64.URLEncoding.EncodeToString(key)
	password := "test-only-password-é"
	hash, err := credentials.HashPassword(password)
	if err != nil {
		panic(err)
	}
	token, err := credentials.Encrypt([]byte("public test payload: é"), encoded)
	if err != nil {
		panic(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(map[string]string{"go_argon2id": hash, "go_fernet_token": string(token)}); err != nil {
		panic(err)
	}
}
