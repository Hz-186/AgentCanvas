package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	authdomain "agentcanvas/internal/domain/auth"
)

type GitHubClient struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	HTTPClient   *http.Client
}

type GitHubUser = authdomain.GitHubUser
type tokenResponse = authdomain.GitHubOAuthToken

type githubEmail struct {
	Email      string `json:"email"`
	Primary    bool   `json:"primary"`
	Verified   bool   `json:"verified"`
	Visibility string `json:"visibility"`
}

func NewGitHubClient(clientID, clientSecret, redirectURL string, scopes []string) *GitHubClient {
	return &GitHubClient{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *GitHubClient) AuthCodeURL(state string) (string, error) {
	if c.ClientID == "" || c.RedirectURL == "" {
		return "", fmt.Errorf("github oauth is not configured")
	}
	values := url.Values{}
	values.Set("client_id", c.ClientID)
	values.Set("redirect_uri", c.RedirectURL)
	values.Set("state", state)
	if len(c.Scopes) > 0 {
		values.Set("scope", strings.Join(c.Scopes, " "))
	}
	return "https://github.com/login/oauth/authorize?" + values.Encode(), nil
}

func (c *GitHubClient) ExchangeCode(ctx context.Context, code string) (*tokenResponse, error) {
	if c.ClientID == "" || c.ClientSecret == "" || c.RedirectURL == "" {
		return nil, fmt.Errorf("github oauth is not configured")
	}
	values := url.Values{}
	values.Set("client_id", c.ClientID)
	values.Set("client_secret", c.ClientSecret)
	values.Set("code", code)
	values.Set("redirect_uri", c.RedirectURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", bytes.NewBufferString(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github token exchange failed: %s", resp.Status)
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, err
	}
	if token.Error != "" {
		return nil, fmt.Errorf("github token exchange error: %s", token.Description)
	}
	return &token, nil
}

func (c *GitHubClient) GetUser(ctx context.Context, accessToken string) (*GitHubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github user request failed: %s", resp.Status)
	}
	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	if user.Email == "" {
		if email, err := c.GetPrimaryEmail(ctx, accessToken); err == nil {
			user.Email = email
		}
	}
	return &user, nil
}

func (c *GitHubClient) GetPrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github emails request failed: %s", resp.Status)
	}
	var emails []githubEmail
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", err
	}
	for _, email := range emails {
		if email.Primary && email.Verified {
			return email.Email, nil
		}
	}
	for _, email := range emails {
		if email.Verified {
			return email.Email, nil
		}
	}
	return "", nil
}
