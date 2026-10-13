package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/jihuayu/registryctl/internal/client"
	"github.com/jihuayu/registryctl/internal/model"
	"github.com/jihuayu/registryctl/internal/oci"
)

var version = "dev"
var commit = "unknown"
var buildDate = "unknown"

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("使用 registryctl login / push / upload / logout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Printf("registryctl %s (%s, %s)\n", version, commit, buildDate)
		return nil
	case "help", "--help", "-h":
		fmt.Println("registryctl：通过 Account 登录并将 OCI 镜像直接上传 R2")
		fmt.Println("用法：registryctl login REGISTRY | push OCI_SOURCE REGISTRY/REPO:TAG | upload status/resume/abort | grant | logout | version")
		fmt.Println("CI：设置 REGISTRY_ACCOUNT_CLIENT_ID / REGISTRY_ACCOUNT_CLIENT_SECRET 后直接执行 push，无需 login 或桌面凭据库")
		return nil
	case "login":
		return login(ctx, args[1:])
	case "logout":
		if len(args) != 2 {
			return errors.New("使用 registryctl logout REGISTRY")
		}
		b, e := client.Base(args[1])
		if e != nil {
			return e
		}
		return client.Logout(b)
	case "push":
		if len(args) != 3 {
			return errors.New("使用 registryctl push OCI_SOURCE REGISTRY/REPO:TAG")
		}
		named, e := reference.ParseNormalizedNamed(args[2])
		if e != nil {
			return e
		}
		tag, ok := named.(reference.Tagged)
		if !ok {
			return errors.New("目标镜像需要 tag")
		}
		base, e := client.Base(reference.Domain(named))
		if e != nil {
			return e
		}
		api, e := authenticated(base)
		if e != nil {
			return e
		}
		image, e := oci.Load(args[1])
		if e != nil {
			return e
		}
		defer image.Cleanup()
		repo := reference.Path(named)
		for _, blob := range image.Blobs {
			if _, e = api.PushBlob(ctx, repo, blob, progress); e != nil {
				return e
			}
		}
		for _, manifest := range image.Manifests {
			if e = api.Manifest(ctx, repo, manifest.Descriptor.Digest.String(), manifest.Descriptor.MediaType, manifest.Manifest); e != nil {
				return e
			}
		}
		if e = api.Manifest(ctx, repo, tag.Tag(), image.Root.Descriptor.MediaType, image.Root.Manifest); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"repository": repo, "tag": tag.Tag(), "digest": image.Root.Descriptor.Digest.String()})
	case "upload":
		return upload(ctx, args[1:])
	case "grant":
		return grant(ctx, args[1:])
	default:
		return errors.New("unknown registryctl command")
	}
}
func progress(message string) { fmt.Fprintln(os.Stderr, message) }
func authenticated(base string) (*client.Client, error) {
	if os.Getenv("REGISTRY_ACCOUNT_CLIENT_ID") != "" && os.Getenv("REGISTRY_ACCOUNT_CLIENT_SECRET") != "" {
		api := &client.Client{Base: base, HTTP: client.HTTPClient()}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cap, e := api.Capabilities(ctx)
		if e != nil {
			return nil, e
		}
		issuer := os.Getenv("REGISTRY_ACCOUNT_ISSUER")
		if issuer == "" {
			issuer = "https://account.jihuayu.com"
		}
		if cap.Issuer != strings.TrimRight(issuer, "/") {
			return nil, errors.New("服务端 issuer 与可信配置不一致")
		}
		credentials := client.Credentials{Registry: base, Issuer: issuer, Audience: cap.Audience, Mode: "machine", Scope: os.Getenv("REGISTRY_ACCOUNT_SCOPE")}
		api.Token = api.Provider(&credentials)
		return api, nil
	}
	c, e := client.Load(base)
	if e != nil {
		return nil, e
	}
	api := &client.Client{Base: base, HTTP: client.HTTPClient()}
	api.Token = api.Provider(&c)
	return api, nil
}
func login(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("使用 registryctl login REGISTRY [--machine | --token-stdin]")
	}
	base, e := client.Base(args[0])
	if e != nil {
		return e
	}
	f := flag.NewFlagSet("login", flag.ContinueOnError)
	machine := f.Bool("machine", false, "Account 机器认证")
	stdin := f.Bool("token-stdin", false, "从 stdin 读取 Access Token")
	issuer := f.String("issuer", "https://account.jihuayu.com", "可信 Account issuer")
	scope := f.String("scope", "registry.pull registry.push", "申请的 Account scope")
	if e = f.Parse(args[1:]); e != nil {
		return e
	}
	if *machine && *stdin {
		return errors.New("machine 和 token-stdin 不能同时使用")
	}
	api := &client.Client{Base: base, HTTP: client.HTTPClient()}
	cap, e := api.Capabilities(ctx)
	if e != nil {
		return e
	}
	if !cap.Direct || cap.Issuer != strings.TrimRight(*issuer, "/") {
		return errors.New("服务端声明的 issuer 与可信配置不一致")
	}
	u, e := url.Parse(cap.Issuer)
	if e != nil || u.Scheme != "https" {
		return errors.New("Account requires HTTPS")
	}
	c := client.Credentials{Registry: base, Issuer: cap.Issuer, Audience: cap.Audience, ClientID: cap.ClientID}
	c.Scope = *scope
	if *machine {
		c.Mode = "machine"
	} else if *stdin {
		b, e := io.ReadAll(io.LimitReader(os.Stdin, 16385))
		if e != nil {
			return e
		}
		if len(b) > 16384 {
			return errors.New("Token too large")
		}
		c.Mode = "token"
		c.Access = strings.TrimSpace(string(b))
		c.Expires = time.Now().Add(5 * time.Minute)
	} else {
		authCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		c, e = api.BrowserLogin(authCtx, cap, *scope, func(u string) { fmt.Fprintln(os.Stderr, "请在浏览器打开：", u) })
		if e != nil {
			return e
		}
	}
	api.Token = api.Provider(&c)
	var identity model.Identity
	if _, e = api.Request(ctx, "GET", "/api/registry/v1/profile", nil, &identity, nil); e != nil {
		return e
	}
	if c.Mode == "token" {
		c.Expires = time.Unix(identity.Expires, 0)
	}
	if *machine {
		fmt.Fprintln(os.Stdout, "机器凭据已验证；后续命令继续从环境读取 Secret")
		return nil
	}
	if e = client.Save(c); e != nil {
		return e
	}
	fmt.Fprintln(os.Stdout, "已登录", base)
	return nil
}

func grant(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return errors.New("使用 registryctl grant REPOSITORY --registry REGISTRY --subject SUBJECT --actions pull,push")
	}
	f := flag.NewFlagSet("grant", flag.ContinueOnError)
	reg := f.String("registry", "", "Registry 地址")
	subject := f.String("subject", "", "Account 主体")
	actions := f.String("actions", "pull", "逗号分隔的仓库权限；空字符串撤销")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	base, e := client.Base(*reg)
	if e != nil || *reg == "" || *subject == "" {
		return errors.New("registry 与 subject 必填")
	}
	api, e := authenticated(base)
	if e != nil {
		return e
	}
	var identity model.Identity
	if _, e = api.Request(ctx, "GET", "/api/registry/v1/profile", nil, &identity, nil); e != nil {
		return e
	}
	identity.Subject = *subject
	identity.Scope = ""
	identity.Expires = 0
	identity.Machine = strings.HasPrefix(*subject, "service:")
	list := []string{}
	if *actions != "" {
		list = strings.Split(*actions, ",")
	}
	_, e = api.Request(ctx, "PUT", "/api/registry/v1/grants", model.Grant{Identity: identity, Repository: args[0], Actions: list}, nil, nil)
	return e
}
func upload(ctx context.Context, args []string) error {
	if len(args) < 3 {
		return errors.New("使用 registryctl upload status|resume|abort SESSION_ID --registry REGISTRY [--source OCI_SOURCE]")
	}
	op, id := args[0], args[1]
	f := flag.NewFlagSet("upload", flag.ContinueOnError)
	reg := f.String("registry", "", "Registry 地址")
	source := f.String("source", "", "OCI 输入")
	if e := f.Parse(args[2:]); e != nil {
		return e
	}
	base, e := client.Base(*reg)
	if e != nil || *reg == "" {
		return errors.New("需要 --registry")
	}
	api, e := authenticated(base)
	if e != nil {
		return e
	}
	switch op {
	case "status":
		status, e := api.Status(ctx, id)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	case "abort":
		_, e = api.Request(ctx, "DELETE", "/api/registry/v1/uploads/"+id, nil, nil, nil)
		return e
	case "resume":
		image, e := oci.Load(*source)
		if e != nil {
			return e
		}
		defer image.Cleanup()
		status, e := api.Status(ctx, id)
		if e != nil {
			return e
		}
		for _, blob := range image.Blobs {
			if blob.Descriptor.Digest.String() == status.Session.Digest {
				result, e := api.ResumeBlob(ctx, id, blob, progress)
				if e != nil {
					return e
				}
				return json.NewEncoder(os.Stdout).Encode(result)
			}
		}
		return errors.New("OCI 输入中没有会话对应的 blob")
	default:
		return errors.New("unknown upload command")
	}
}
