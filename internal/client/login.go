package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/jihuayu/registryctl/internal/account"
	"github.com/jihuayu/registryctl/internal/model"
)

type Capabilities struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	ClientID string `json:"cli_client_id"`
	Direct   bool   `json:"direct_upload"`
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var cap Capabilities
	_, e := c.Request(ctx, "GET", "/api/registry/v1/capabilities", nil, &cap, nil)
	return cap, e
}

// BrowserLogin 使用固定回环端口，Account 当前设备授权不携带资源 audience。
func (c *Client) BrowserLogin(ctx context.Context, cap Capabilities, scope string, open func(string)) (Credentials, error) {
	if cap.ClientID == "" {
		return Credentials{}, errors.New("CLI client is not registered")
	}
	listen, e := net.Listen("tcp", "127.0.0.1:17893")
	if e != nil {
		return Credentials{}, errors.New("本地登录端口 17893 不可用")
	}
	defer listen.Close()
	redirect := "http://127.0.0.1:17893/callback"
	verifier, state := model.Random(), model.Random()
	hash := sha256.Sum256([]byte(verifier))
	authorize := cap.Issuer + "/oauth/authorize?" + url.Values{"client_id": {cap.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"openid profile offline_access " + scope}, "audience": {cap.Audience}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {state}}.Encode()
	codes := make(chan string, 1)
	var used atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("state") != state || r.URL.Query().Get("iss") != cap.Issuer || r.URL.Query().Get("code") == "" || !used.CompareAndSwap(false, true) {
			http.Error(w, "登录回调无效", 400)
			return
		}
		select {
		case codes <- r.URL.Query().Get("code"):
			http.Redirect(w, r, "/complete", http.StatusSeeOther)
		default:
			http.Error(w, "登录回调已使用", 409)
		}
	})
	mux.HandleFunc("/complete", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "登录完成，可以关闭此页。") })
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(listen) }()
	defer srv.Close()
	open(authorize)
	select {
	case <-ctx.Done():
		return Credentials{}, ctx.Err()
	case code := <-codes:
		var token account.Token
		e = OAuthPost(ctx, c.HTTP, cap.Issuer+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {cap.ClientID}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}, &token)
		if e != nil {
			return Credentials{}, e
		}
		if token.Access == "" || token.ExpiresIn <= 0 {
			return Credentials{}, errors.New("invalid Account token response")
		}
		return Credentials{Registry: c.Base, Issuer: cap.Issuer, Audience: cap.Audience, ClientID: cap.ClientID, Mode: "user", Access: token.Access, Refresh: token.Refresh, Expires: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)}, nil
	}
}
