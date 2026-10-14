package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jihuayu/registryctl/internal/account"
	"github.com/zalando/go-keyring"
)

type Credentials struct {
	Registry, Issuer, Audience, ClientID, Mode, Access, Refresh string
	Scope                                                       string
	Expires                                                     time.Time
}
type Client struct {
	Base  string
	HTTP  *http.Client
	Token func(context.Context) (string, error)
	Mu    sync.Mutex
}
type Error struct {
	Status int
	Code   string
}

func (e Error) Error() string {
	return fmt.Sprintf("registry request rejected (%d): %s", e.Status, e.Code)
}
func HTTPClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func Base(input string) (string, error) {
	if !strings.Contains(input, "://") {
		input = "https://" + input
	}
	u, e := url.Parse(input)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return "", errors.New("invalid registry URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
		return "", errors.New("registry requires HTTPS")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func ObjectURL(value string) error {
	u, e := url.Parse(value)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("invalid object URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
		return errors.New("object URL requires HTTPS")
	}
	return nil
}
func (c *Client) Request(ctx context.Context, method, path string, in, out any, headers map[string]string) (int, error) {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return 0, e
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if e != nil {
		return 0, e
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "registryctl/0.1")
	if c.Token != nil {
		t, e := c.Token(ctx)
		if e != nil {
			return 0, e
		}
		r.Header.Set("Authorization", "Bearer "+t)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	h := c.HTTP
	if h == nil {
		h = HTTPClient()
	}
	resp, e := h.Do(r)
	if e != nil {
		return 0, errors.New("registry connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var f struct {
			Code string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&f)
		return resp.StatusCode, Error{resp.StatusCode, f.Code}
	}
	if out != nil && resp.StatusCode != 204 {
		e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
	}
	return resp.StatusCode, e
}
func Load(base string) (Credentials, error) {
	b, e := keyring.Get("registryctl", base)
	if e != nil {
		return Credentials{}, errors.New("尚未登录或系统凭据库不可用")
	}
	var c Credentials
	e = json.Unmarshal([]byte(b), &c)
	if e == nil && c.Registry != base {
		return c, errors.New("credential registry mismatch")
	}
	return c, e
}
func Save(c Credentials) error {
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	if e = keyring.Set("registryctl", c.Registry, string(b)); e != nil {
		return errors.New("系统凭据库不可用，拒绝明文保存凭据")
	}
	return nil
}
func Logout(base string) error { return keyring.Delete("registryctl", base) }
func (c *Client) Provider(credentials *Credentials) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		c.Mu.Lock()
		defer c.Mu.Unlock()
		if credentials.Mode == "machine" || credentials.Mode == "github-oidc" {
			if time.Now().Add(30 * time.Second).Before(credentials.Expires) {
				return credentials.Access, nil
			}
			ac := account.Client{Issuer: credentials.Issuer, Audience: credentials.Audience, HTTP: c.HTTP}
			scope := credentials.Scope
			if scope == "" {
				scope = "registry.pull registry.push"
			}
			var t account.Token
			var e error
			if credentials.Mode == "github-oidc" {
				t, e = c.githubToken(ctx, credentials, scope)
			} else {
				t, e = ac.Machine(ctx, os.Getenv("REGISTRY_ACCOUNT_CLIENT_ID"), os.Getenv("REGISTRY_ACCOUNT_CLIENT_SECRET"), scope)
			}
			if e != nil {
				return "", e
			}
			credentials.Access = t.Access
			credentials.Expires = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
			return t.Access, nil
		}
		if time.Now().Add(30 * time.Second).Before(credentials.Expires) {
			return credentials.Access, nil
		}
		if credentials.Refresh == "" {
			return "", errors.New("登录 Token 已过期，请重新登录")
		}
		var t account.Token
		e := OAuthPost(ctx, c.HTTP, credentials.Issuer+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {credentials.ClientID}, "refresh_token": {credentials.Refresh}}, &t)
		if e != nil {
			return "", e
		}
		credentials.Access = t.Access
		credentials.Refresh = t.Refresh
		credentials.Expires = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
		if e = Save(*credentials); e != nil {
			return "", e
		}
		return credentials.Access, nil
	}
}
func OAuthPost(ctx context.Context, h *http.Client, endpoint string, form url.Values, out any) error {
	if h == nil {
		h = HTTPClient()
	}
	r, e := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("User-Agent", "registryctl/0.1")
	r.Header.Set("Accept", "application/json")
	resp, e := h.Do(r)
	if e != nil {
		return errors.New("Account connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("Account authorization rejected")
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
