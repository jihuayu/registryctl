package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubOIDCExchangeAndRenewalWithoutClientSecret(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("REGISTRY_ACCOUNT_CLIENT_SECRET", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://run-actions-fixture.actions.githubusercontent.com/idtoken?api-version=2")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "fixture-runner-credential")
	var requests atomic.Int32
	h := HTTPClient()
	h.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		if r.URL.Host == "run-actions-fixture.actions.githubusercontent.com" {
			if r.Header.Get("Authorization") != "Bearer fixture-runner-credential" || r.URL.Query().Get("audience") != "https://account.example.test/g/github/github-actions/fixture-ci" {
				t.Error("GitHub 取证凭据或 audience 错误")
			}
			body = `{"value":"fixture-github-jwt"}`
		} else if r.URL.Host == "account.example.test" {
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "fixture-ci" || r.Form.Get("subject_token") != "fixture-github-jwt" || r.Form.Get("audience") != "https://registry.example.test" || r.Form.Has("client_secret") || r.Header.Get("Authorization") != "" {
				t.Error("Account 交换参数或凭据隔离错误")
			}
			requests.Add(1)
			body = `{"access_token":"fixture-account-access","token_type":"Bearer","expires_in":300}`
		} else {
			t.Error("意外请求主机")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	c := &Client{HTTP: h}
	credentials := &Credentials{Registry: "https://registry.example.test", Issuer: "https://account.example.test/g/github", Audience: "https://registry.example.test", ClientID: "fixture-ci", Mode: "github-oidc"}
	provider := c.Provider(credentials)
	for range 2 {
		token, e := provider(context.Background())
		if e != nil || token != "fixture-account-access" {
			t.Fatal(token, e)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("有效短期令牌未复用")
	}
	credentials.Expires = credentials.Expires.Add(-time.Hour)
	if _, e := provider(context.Background()); e != nil || requests.Load() != 2 {
		t.Fatal("未重新获取 GitHub assertion 续期", e)
	}
}

func TestGitHubRunnerCredentialNeverFollowsUntrustedBroker(t *testing.T) {
	for _, endpoint := range []string{"http://run.actions.githubusercontent.com/idtoken", "https://evil.example.test/idtoken", "https://actions.githubusercontent.com.evil.example.test/idtoken", "https://user@run.actions.githubusercontent.com/idtoken", "https://run.actions.githubusercontent.com:8443/idtoken"} {
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", endpoint)
		h := HTTPClient()
		h.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Error("Runner 凭据被发送到未授权地址")
			return nil, nil
		})
		c := &Client{HTTP: h}
		if _, e := c.githubAssertion(context.Background(), &Credentials{Issuer: "https://account.example.test", ClientID: "ci"}); e == nil {
			t.Fatal("不可信 Broker 被接受", endpoint)
		}
	}
}
