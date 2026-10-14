新增 GitHub Actions OIDC：使用 Runner 签名身份关联 Account 中已绑定的 GitHub 用户，按精确仓库和 workflow 策略换取短期令牌。仅需公开 Client ID 和 id-token: write，无需 Account Client Secret 或 R2 Key，上传期间自动取新 assertion 续期。

新增 registryctl docker-login，用当前认证方式为原生 Docker 配置短期令牌。README 提供 curl 安装和免 Secret 工作流示例，原有用户登录及 M2M 客户端继续兼容。

包含 Linux、macOS、Windows 的 amd64/arm64 二进制包、SHA256SUMS、依赖许可证与构建提交信息。可使用仓库根目录的 GitHub Composite Action 安装并推送镜像；CI 凭据从环境读取，不依赖桌面凭据库，不需要 R2 Key。

上传使用 registryctl，不接管原生 docker push，不接受 docker save archive 冒充 OCI archive。独立校验和超时策略由 Registry 服务端负责，上传完成后仍需等待可信校验。
