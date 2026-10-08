package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	serviceAccountPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+@[a-zA-Z0-9.-]+\.iam\.gserviceaccount\.com$`)
	projectPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
)

const (
	fcmScope = "https://www.googleapis.com/auth/firebase.messaging"
	// accessMargin renews an access token this long before it expires, so a
	// token is never presented as it lapses.
	accessMargin = 5 * time.Minute
)

// FCM sends through the Firebase Cloud Messaging HTTP v1 API, authorized by
// an OAuth 2.0 access token that a service-account JWT is exchanged for.
type FCM struct {
	project, email string
	key            *rsa.PrivateKey

	Client            *http.Client
	TokenURL, SendURL string

	mu      sync.Mutex
	access  string
	expires time.Time
	now     func() time.Time
}

// NewFCM loads a Firebase service account's JSON key, whose project receives
// the app's registrations.
func NewFCM(keyJSON []byte) (*FCM, error) {
	var account struct {
		Type    string `json:"type"`
		Project string `json:"project_id"`
		Email   string `json:"client_email"`
		Key     string `json:"private_key"`
	}
	invalid := fmt.Errorf("%w: DONOR_PUSH_FCM_KEY_FILE must be a Firebase service-account JSON key", ErrConfiguration)
	if json.Unmarshal(keyJSON, &account) != nil || account.Type != "service_account" || !projectPattern.MatchString(account.Project) || !serviceAccountPattern.MatchString(account.Email) {
		return nil, invalid
	}
	block, rest := pem.Decode([]byte(account.Key))
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, invalid
	}
	parsed, _ := x509.ParsePKCS8PrivateKey(block.Bytes)
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 {
		return nil, invalid
	}
	return &FCM{
		project: account.Project, email: account.Email, key: key, Client: newClient(),
		TokenURL: "https://oauth2.googleapis.com/token", SendURL: "https://fcm.googleapis.com",
		now: time.Now,
	}, nil
}

// accessToken returns a cached access token, exchanging a newly signed
// assertion for one when it is missing or about to expire.
func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.access != "" && now.Before(f.expires) {
		return f.access, nil
	}
	assertion, err := jwt(map[string]string{"alg": "RS256", "typ": "JWT"}, map[string]any{
		"iss": f.email, "scope": fcmScope, "aud": f.TokenURL, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}, func(digest []byte) ([]byte, error) {
		return rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest)
	})
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := f.Client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var grant struct {
		Token   string `json:"access_token"`
		Expires int64  `json:"expires_in"`
	}
	body := readResponse(response)
	if err := throttled(response); err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &grant) != nil || grant.Token == "" || time.Duration(grant.Expires)*time.Second <= accessMargin {
		return "", ErrRejected
	}
	f.access, f.expires = grant.Token, now.Add(time.Duration(grant.Expires)*time.Second-accessMargin)
	return f.access, nil
}

// forget discards an access token FCM refused, so the next send exchanges
// another.
func (f *FCM) forget(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.access == token {
		f.access = ""
	}
}

func (f *FCM) Send(ctx context.Context, device Device, n Notification) error {
	ttl := max(n.Expires.Sub(f.now()), 0)
	body, err := json.Marshal(map[string]any{"message": map[string]any{
		"token":        device.Token,
		"notification": map[string]string{"title": n.Title, "body": n.Body},
		"data":         map[string]string{"category": n.Category, "path": n.Path},
		"android": map[string]any{
			"collapse_key": n.ID,
			"ttl":          strconv.FormatInt(int64(ttl/time.Second), 10) + "s",
			"notification": map[string]string{"tag": n.ID},
		},
	}})
	if err != nil {
		return err
	}
	token, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.SendURL+"/v1/projects/"+f.project+"/messages:send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := f.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var reply struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				ErrorCode       string `json:"errorCode"`
				FieldViolations []struct {
					Field string `json:"field"`
				} `json:"fieldViolations"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(readResponse(response), &reply)
	if response.StatusCode == http.StatusOK {
		return nil
	}
	// Only a verdict on the token itself drops it: UNREGISTERED, or an
	// invalid argument located at the token. SENDER_ID_MISMATCH is left to
	// configuration, since a wrong project would otherwise drop every token.
	for _, detail := range reply.Error.Details {
		if detail.ErrorCode == "UNREGISTERED" {
			return ErrUnregistered
		}
		for _, violation := range detail.FieldViolations {
			if reply.Error.Status == "INVALID_ARGUMENT" && violation.Field == "message.token" {
				return ErrUnregistered
			}
		}
	}
	if response.StatusCode == http.StatusUnauthorized {
		f.forget(token)
		return Unavailable{}
	}
	if err := throttled(response); err != nil {
		return err
	}
	return ErrRejected
}
