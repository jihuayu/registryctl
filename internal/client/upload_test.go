package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jihuayu/registryctl/internal/model"
	"github.com/jihuayu/registryctl/internal/oci"
	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// 控制面与对象服务分开，拒绝把 Account 凭据或镜像字节发送到错误路径。
func TestMultipartDirectUploadResumeAndCredentialIsolation(t *testing.T) {
	data := bytes.Repeat([]byte("independent object content"), 512)
	d := digest.FromBytes(data)
	path := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	uploaded := []byte{}
	var objectPuts atomic.Int32
	object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.Header.Get("Authorization") != "" {
			t.Error("对象服务收到错误请求或 Account 凭据")
			w.WriteHeader(400)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		uploaded = append(uploaded, body...)
		mu.Unlock()
		objectPuts.Add(1)
		w.Header().Set("ETag", `"fixture-etag"`)
	}))
	defer object.Close()
	session := model.Session{ID: "fixture-upload", Repository: "home/app", Digest: d.String(), Size: int64(len(data)), PartSize: int64(len(data)), PartCount: 1, State: "uploading"}
	state := "uploading"
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("控制面没有认证")
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "HEAD":
			w.WriteHeader(404)
		case r.Method == "POST" && r.URL.Path == "/api/registry/v1/uploads":
			_ = json.NewEncoder(w).Encode(session)
		case strings.HasSuffix(r.URL.Path, "/parts/sign"):
			_ = json.NewEncoder(w).Encode([]model.SignedPart{{Number: 1, URL: object.URL + "/part?signature=fixture"}})
		case strings.HasSuffix(r.URL.Path, "/seal"):
			mu.Lock()
			matched := bytes.Equal(uploaded, data)
			mu.Unlock()
			if !matched {
				t.Error("封存前对象内容不完整")
			}
			state = "verified"
			session.State = state
			_ = json.NewEncoder(w).Encode(session)
		case strings.HasSuffix(r.URL.Path, "/commit"):
			if state != "verified" {
				t.Error("未校验对象提交")
			}
			state = "committed"
			session.State = state
			_ = json.NewEncoder(w).Encode(session)
		case r.Method == "GET":
			session.State = state
			_ = json.NewEncoder(w).Encode(Status{Session: session})
		default:
			t.Error("意外控制面请求")
			w.WriteHeader(400)
		}
	}))
	defer control.Close()
	c := &Client{Base: control.URL, HTTP: HTTPClient(), Token: func(context.Context) (string, error) { return "fixture-token", nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	blob := oci.Blob{Path: path, Descriptor: v1.Descriptor{Digest: d, Size: int64(len(data)), MediaType: v1.MediaTypeImageLayer}}
	s, err := c.PushBlob(ctx, "home/app", blob, func(string) {})
	if err != nil || s.State != "committed" || objectPuts.Load() != 1 {
		t.Fatal(s, err, objectPuts.Load())
	}
	if _, err = c.ResumeBlob(ctx, s.ID, blob, func(string) {}); err != nil || objectPuts.Load() != 1 {
		t.Fatal("恢复已提交会话再次上传", err)
	}
	blob.Descriptor.Size++
	if _, err = c.ResumeBlob(ctx, s.ID, blob, func(string) {}); err == nil {
		t.Fatal("不同恢复源被接受")
	}
}

func TestMachineProviderDoesNotUseDesktopKeyring(t *testing.T) {
	var calls atomic.Int32
	account := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("audience") != "registry-fixture" || r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_secret") != "fixture-secret" {
			t.Error("机器授权参数错误")
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "token_type": "Bearer", "expires_in": 300})
	}))
	defer account.Close()
	t.Setenv("REGISTRY_ACCOUNT_CLIENT_ID", "fixture-ci")
	t.Setenv("REGISTRY_ACCOUNT_CLIENT_SECRET", "fixture-secret")
	c := &Client{HTTP: HTTPClient()}
	credentials := &Credentials{Issuer: account.URL, Audience: "registry-fixture", Mode: "machine"}
	provider := c.Provider(credentials)
	for range 2 {
		token, err := provider(context.Background())
		if err != nil || token != "fixture-access" {
			t.Fatal(token, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("有效机器 Token 未复用")
	}
}
