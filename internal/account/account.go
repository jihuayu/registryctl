package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jihuayu/registryctl/internal/model"
)

type Client struct {
	Issuer, Audience, Group, ClientID, Secret string
	HTTP                                      *http.Client
}

func (c *Client) post(ctx context.Context, path string, values url.Values, out any) error {
	r, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.Issuer, "/")+path, strings.NewReader(values.Encode()))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("User-Agent", "registry/0.1")
	r.Header.Set("Accept", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, e := client.Do(r)
	if e != nil {
		return errors.New("Account unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("Account authorization rejected")
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
func (c *Client) Authenticate(ctx context.Context, token string) (model.Identity, error) {
	if token == "" || len(token) > 16384 {
		return model.Identity{}, errors.New("credential required")
	}
	var out struct {
		model.Identity
		Active   bool   `json:"active"`
		Audience string `json:"aud"`
	}
	e := c.post(ctx, "/oauth/introspect", url.Values{"token": {token}, "client_id": {c.ClientID}, "client_secret": {c.Secret}}, &out)
	i := out.Identity
	if e != nil {
		return i, e
	}
	if !out.Active || i.Issuer != c.Issuer || out.Audience != c.Audience || i.Group != c.Group || i.Subject == "" || i.Expires <= time.Now().Unix() {
		return model.Identity{}, errors.New("invalid Account token")
	}
	return i, nil
}

type Token struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int    `json:"expires_in"`
	Type      string `json:"token_type"`
}

func (c *Client) Exchange(ctx context.Context, form url.Values) (Token, error) {
	var t Token
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.Secret)
	e := c.post(ctx, "/oauth/token", form, &t)
	if e == nil && (t.Access == "" || t.ExpiresIn <= 0) {
		e = errors.New("invalid Account token response")
	}
	return t, e
}

func (c *Client) Machine(ctx context.Context, id, secret, scope string) (Token, error) {
	var t Token
	e := c.post(ctx, "/oauth/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {id}, "client_secret": {secret}, "audience": {c.Audience}, "scope": {scope}}, &t)
	if e == nil && (t.Access == "" || !strings.EqualFold(t.Type, "Bearer")) {
		e = errors.New("invalid token response")
	}
	return t, e
}
