// CLAUDE:SUMMARY OAuth2 Google provider configuration and user profile fetching.
// CLAUDE:DEPENDS horosafe
// CLAUDE:EXPORTS OAuthConfig, OAuthUser, NewGoogleProvider, FetchGoogleUser

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hazyhaar/pkg/horosafe"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// OAuthConfig holds the configuration needed to set up an OAuth2 provider.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// OAuthUser represents the normalized user profile returned by an OAuth2 provider.
type OAuthUser struct {
	ProviderUserID string
	Email          string
	Name           string
	AvatarURL      string
}

// NewGoogleProvider returns an oauth2.Config configured for Google login
// with email and profile scopes.
func NewGoogleProvider(cfg OAuthConfig) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}
}

// FetchGoogleUser exchanges an OAuth2 token for the user's Google profile.
func FetchGoogleUser(ctx context.Context, oauthCfg *oauth2.Config, code string) (*OAuthUser, *oauth2.Token, error) {
	token, err := oauthCfg.Exchange(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("oauth exchange: %w", err)
	}

	client := oauthCfg.Client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create userinfo request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch google userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := horosafe.LimitedReadAll(resp.Body, horosafe.MaxResponseBody)
		return nil, nil, fmt.Errorf("google userinfo returned %d: %s", resp.StatusCode, body)
	}

	var info struct {
		ID            string `json:"id"`
		Email         string `json:"email"`
		VerifiedEmail bool   `json:"verified_email"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, nil, fmt.Errorf("decode google userinfo: %w", err)
	}

	return &OAuthUser{
		ProviderUserID: info.ID,
		Email:          info.Email,
		Name:           info.Name,
		AvatarURL:      info.Picture,
	}, token, nil
}
