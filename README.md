# registryctl

通过 Account 登录，将 Docker/OCI 镜像内容直接上传 Cloudflare R2，再由 Registry 发布镜像引用。客户端与独立校验器传输镜像层，VPS 仅处理控制请求、权限和 Manifest。

这是独立公开的 CLI 与 GitHub Action 仓库，不依赖私有 home-service 源码、R2 通用 Key、Docker daemon 或 Distribution 构建缓存。已部署的服务为 <https://registry.jihuayu.com>，采用定制 CNCF Distribution 和 NAS 独立 SHA-256 校验；这里公开客户端，不提供服务器部署凭据。

## 下载与安装

[GitHub Releases](https://github.com/jihuayu/registryctl/releases/latest) 提供 Linux、macOS、Windows 的 amd64/arm64 包、`SHA256SUMS` 和构建提交信息。

Linux / macOS 一行安装到 `~/.local/bin`，脚本先验证下载包 SHA-256：

```sh
curl -fsSL https://raw.githubusercontent.com/jihuayu/registryctl/v0.2.0/install.sh |
  sh -s -- --version v0.2.0
export PATH="$HOME/.local/bin:$PATH"
registryctl version
```

也可以先下载、查看脚本，再运行：

```sh
curl -fsSLO https://raw.githubusercontent.com/jihuayu/registryctl/v0.2.0/install.sh
sh install.sh --version v0.2.0 --bin-dir "$HOME/.local/bin"
```

Git Bash 也可使用安装脚本。Windows PowerShell 手动下载 amd64 包并验证：

```powershell
curl.exe -fL https://github.com/jihuayu/registryctl/releases/download/v0.2.0/registryctl_0.2.0_windows_amd64.zip -o registryctl.zip
curl.exe -fL https://github.com/jihuayu/registryctl/releases/download/v0.2.0/SHA256SUMS -o SHA256SUMS
$expected = ((Get-Content SHA256SUMS | Where-Object { $_ -match '  registryctl_0.2.0_windows_amd64.zip$' }) -split '  ')[0]
if ((Get-FileHash registryctl.zip -Algorithm SHA256).Hash.ToLower() -ne $expected) { throw 'SHA-256 不匹配' }
Expand-Archive registryctl.zip -DestinationPath registryctl
.\registryctl\registryctl.exe version
```

arm64 用户选择对应包。安装脚本依赖 curl、tar/unzip 和 sha256sum/shasum；不会修改 shell 配置或请求 sudo。上传使用 OCI 格式，普通 `docker save` 输出的 Docker archive 不受支持。

## 本机推送

```sh
registryctl login registry.jihuayu.com
registryctl push ./image.oci.tar registry.jihuayu.com/home/app:版本
```

登录显示 Account PKCE 授权地址，浏览器完成授权后将刷新凭据保存到系统凭据库。Linux 交互登录需要可用的 Secret Service；没有桌面凭据库的 CI 使用下面的 OIDC 或机器环境变量模式，不保存明文凭据。

OCI 输入可以是目录或 archive；支持单平台 Manifest 和多平台 Index。上传前验证原始 Blob 摘要，复用已存在对象，上传期间流式读文件，并等待服务端独立摘要校验。上传进度输出到 stderr，成功结果的 repository/tag/digest JSON 输出到 stdout。

## GitHub Actions 构建并推送

GitHub Actions 使用 OIDC，无需保存 Account Client Secret 或 R2 Key。先在 Account 绑定自己的 GitHub 账号，再为公开 OAuth 客户端配置 GitHub Actions 策略：

- 控制台 → 应用 → 客户端策略 → GitHub Actions：关联当前用户已绑定的 GitHub 数字 ID。
- 精确填写 repository ID、owner ID、仓库名、`refs/heads/main`、workflow ref 和 environment（未使用时留空）。例如 `OWNER/REPO/.github/workflows/push.yml@refs/heads/main`。
- 为应用授予 Registry API 的机器访问，并只允许 `registry.pull registry.push`；绑定用户也必须具有相应角色和目标仓库 pull/push ACL。
- 客户端 ID 是公开标识，可以写入 YAML 或 Repository Variables；不需要 Client Secret。部署的专用客户端 `registry-github` 只信任明确登记的仓库，不能直接用于其他仓库。

Account 验证 GitHub 签名、audience、固定 actor/repository/owner ID、分支、workflow、环境和事件。仅支持 `push`、`workflow_dispatch`，拒绝 PR 事件。令牌归属绑定的 Account 用户，权限取用户、应用、客户端和策略的交集，最长五分钟，无 refresh token。CLI 使用新的 GitHub assertion 自动续期；解绑 GitHub、禁用账号/客户端或修改策略会撤销相应访问。

以下 workflow 在 main 提交后构建 OCI archive 并直传 R2。先登记自己的仓库策略，再修改客户端 ID、workflow 文件名和镜像路径：

```yaml
name: 构建与推送
on:
  push:
    branches: [main]
permissions:
  contents: read
jobs:
  image:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803 # v6
      - uses: docker/setup-buildx-action@8d2750c68a42422c14e847fe6c8ac0403b4cbd6f # v3
      - uses: docker/build-push-action@10e90e3645eae34f1e60eeb005ba3a3d33f178e8 # v6
        with:
          context: .
          platforms: linux/amd64
          outputs: type=oci,dest=${{ runner.temp }}/image.oci.tar
          provenance: false
          sbom: false
      - name: 直传 R2 并发布
        id: push
        uses: jihuayu/registryctl@v0.2.0
        with:
          source: ${{ runner.temp }}/image.oci.tar
          image: registry.jihuayu.com/home/app:${{ github.sha }}
        env:
          REGISTRY_ACCOUNT_AUTH: github-oidc
          REGISTRY_ACCOUNT_CLIENT_ID: ${{ vars.REGISTRY_ACCOUNT_CLIENT_ID }}
      - name: 显示不可变摘要
        env:
          DIGEST: ${{ steps.push.outputs.digest }}
        run: printf '%s\n' "$DIGEST"
```

Action 参数：

| 参数 | 含义 |
| --- | --- |
| version | 留空使用 Action 对应 VERSION；可指定完整版本，例如 v0.2.0 |
| source | OCI archive 或 layout 目录 |
| image | 完整 REGISTRY/REPOSITORY:TAG |

同时省略 source/image 时只安装，把 registryctl 加入后续 step 的 PATH；推送时两项必须同时指定。`digest`、`image` 输出只有推送成功后才设置。支持 `@v1` 主版本别名，复现构建建议固定完整版本或提交 SHA；发布包信息会记录构建提交。

已有 M2M 客户端仍兼容 Client Secret，可在普通 CI 脚本中使用：

```sh
export REGISTRY_ACCOUNT_CLIENT_ID='机器客户端 ID'
# REGISTRY_ACCOUNT_CLIENT_SECRET 由 CI secret 注入，不在这里赋值。
registryctl push ./image.oci.tar registry.jihuayu.com/home/app:版本
```

机器 Token 在进程内自动续期，无需执行 login。默认 issuer 为 `https://account.jihuayu.com`；自建兼容 Account 可显式设置 `REGISTRY_ACCOUNT_ISSUER`，可用 `REGISTRY_ACCOUNT_SCOPE` 缩小所申请的 scope。

## 恢复与原生拉取

```sh
registryctl upload status SESSION_ID --registry registry.jihuayu.com
registryctl upload resume SESSION_ID --registry registry.jihuayu.com --source ./image.oci.tar
registryctl upload abort SESSION_ID --registry registry.jihuayu.com
registryctl logout registry.jihuayu.com
```

普通 Docker pull 使用标准 Registry token challenge，Blob 下载重定向 R2。GitHub Actions 可使用 job 独立的 `DOCKER_CONFIG`，将短期令牌交给 Docker；令牌到期后重新登录：

```sh
registryctl docker-login registry.jihuayu.com
docker pull registry.jihuayu.com/home/app:版本
docker logout registry.jihuayu.com
```

命令支持当前 registryctl 登录方式（包括 OIDC），不是长期 Docker credential helper。旧的 M2M Secret 登录仍保留：

```sh
printf '%s' "$REGISTRY_ACCOUNT_CLIENT_SECRET" |
  docker login registry.jihuayu.com --username "$REGISTRY_ACCOUNT_CLIENT_ID" --password-stdin
docker pull registry.jihuayu.com/home/app:版本
```

上传时不走原生 docker push，签名或校验失败也不回退 VPS 代理。当前部署的 NAS 公网链路较慢，较大的单层可能超过服务端 30 分钟校验租约；本地 1 GiB 验收与公网约 58 MB 镜像验收分开记录，不保证当前网络下的公网大层性能。

## 开发与发布

```sh
go test ./...
go vet ./...
python scripts/test-install.py
python scripts/package.py --version v0.2.0
```

源码来自自有客户端的固定提交，见 source.json；独立公开仓库继续维护 CLI。合并并验证后创建与 VERSION 对应的 vMAJOR.MINOR.PATCH 标签，GitHub Actions 自动从该提交生成六个平台附件及校验文件，正式 Release 不覆盖已有附件。

许可证为 [MIT](LICENSE)，依赖许可证见 [THIRD_PARTY.md](THIRD_PARTY.md)，发布包附带实际许可证文本。
