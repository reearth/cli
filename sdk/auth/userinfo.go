package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"

	"github.com/reearth/cli/sdk/config"
)

// FetchUser calls the OIDC userinfo endpoint.
func FetchUser(ctx context.Context, client *http.Client, env *Env, tok *oauth2.Token) (*config.User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.Domain+"/userinfo", nil)
	if err != nil {
		return nil, err
	}
	tok.SetAuthHeader(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo: %s", resp.Status)
	}
	var u struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	return &config.User{Sub: u.Sub, Email: u.Email, Name: u.Name}, nil
}

// Revoke invalidates a refresh token (best effort; used by logout).
func Revoke(ctx context.Context, client *http.Client, env *Env, refreshToken string) error {
	body, _ := json.Marshal(map[string]string{"client_id": env.ClientID, "token": refreshToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.Domain+"/oauth/revoke", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke: %s", resp.Status)
	}
	return nil
}
