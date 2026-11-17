package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// tokenRenewMargin renews the access token this long before it expires.
const tokenRenewMargin = time.Minute

// tokenSource logs in to the OpenEverest API with POST /v1/auth/token.
//
// It uses the password grant once and then renews the access token with the
// refresh token, which the server rotates on every use. Each password grant is
// recorded as a LoginSucceeded event, so logging in again on every renewal
// would fill the audit log with the daemon's own logins and hit the login rate
// limit. The password grant is only repeated when the refresh fails.
type tokenSource struct {
	apiURL   string
	user     string
	password string
	http     *http.Client
	now      func() time.Time

	mu           sync.Mutex
	accessToken  string
	expiry       time.Time
	refreshToken string
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// Token returns a valid access token.
func (s *tokenSource) Token(ctx context.Context) (string, error) {
	if s.user == "" || s.password == "" {
		return "", fmt.Errorf("EVEREST_SERVICE_USER and EVEREST_SERVICE_PASSWORD must be set")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.accessToken != "" && s.now().Before(s.expiry.Add(-tokenRenewMargin)) {
		return s.accessToken, nil
	}
	if s.refreshToken != "" {
		err := s.request(ctx, map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": s.refreshToken,
		})
		if err == nil {
			return s.accessToken, nil
		}
		log.Printf("refreshing the access token failed, logging in again: %v", err)
		s.refreshToken = ""
	}
	if err := s.request(ctx, map[string]string{
		"grant_type": "password",
		"username":   s.user,
		"password":   s.password,
	}); err != nil {
		return "", err
	}
	log.Printf("logged in to the OpenEverest API as %q", s.user)
	return s.accessToken, nil
}

// Invalidate drops the access token, e.g. after the API rejected it, so the
// next Token call renews it.
func (s *tokenSource) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accessToken = ""
}

func (s *tokenSource) request(ctx context.Context, body map[string]string) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL+"/v1/auth/token", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s grant: %w", body["grant_type"], err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s grant: HTTP %d: %s", body["grant_type"], resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return fmt.Errorf("decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return fmt.Errorf("%s grant: no access_token in the response", body["grant_type"])
	}
	s.accessToken = tok.AccessToken
	s.expiry = s.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	s.refreshToken = tok.RefreshToken
	return nil
}
