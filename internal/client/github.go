package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/jihuayu/registryctl/internal/account"
)

// GitHubOIDCAvailable 检查 Runner 提供的能力，不从源码文件读取 JWT。
func GitHubOIDCAvailable() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL") != "" && os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN") != ""
}

func (c *Client) githubToken(ctx context.Context, credentials *Credentials, scope string) (account.Token, error) {
	var token account.Token
	if !GitHubOIDCAvailable() || credentials.ClientID == "" {
		return token, errors.New("GitHub OIDC 需要 Actions job 的 id-token: write 和 REGISTRY_ACCOUNT_CLIENT_ID")
	}
	assertion, e := c.githubAssertion(ctx, credentials)
	if e != nil {
		return token, e
	}
	e = OAuthPost(ctx, c.HTTP, strings.TrimRight(credentials.Issuer, "/")+"/oauth/token", url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"client_id":  {credentials.ClientID}, "audience": {credentials.Audience}, "scope": {scope},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"}, "subject_token": {assertion},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
	}, &token)
	if e == nil && (token.Access == "" || token.ExpiresIn <= 0 || token.ExpiresIn > 300 || !strings.EqualFold(token.Type, "Bearer")) {
		e = errors.New("Account OIDC 换取令牌响应无效")
	}
	return token, e
}

func (c *Client) githubAssertion(ctx context.Context, credentials *Credentials) (string, error) {
	u, e := url.Parse(os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"))
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || !strings.HasSuffix(u.Hostname(), ".actions.githubusercontent.com") {
		return "", errors.New("GitHub OIDC 取证地址无效，拒绝发送 Runner 凭据")
	}
	query := u.Query()
	query.Set("audience", strings.TrimRight(credentials.Issuer, "/")+"/github-actions/"+credentials.ClientID)
	u.RawQuery = query.Encode()
	r, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return "", errors.New("GitHub OIDC 请求无效")
	}
	r.Header.Set("Authorization", "Bearer "+os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
	r.Header.Set("User-Agent", "registryctl/github-oidc")
	r.Header.Set("Accept", "application/json")
	h := c.HTTP
	if h == nil {
		h = HTTPClient()
	}
	response, e := h.Do(r)
	if e != nil {
		return "", errors.New("GitHub OIDC 取证失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("GitHub OIDC 取证被拒绝，请检查 id-token: write")
	}
	var result struct {
		Value string `json:"value"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result) != nil || result.Value == "" || len(result.Value) > 16384 {
		return "", errors.New("GitHub OIDC 响应无效")
	}
	return result.Value, nil
}
