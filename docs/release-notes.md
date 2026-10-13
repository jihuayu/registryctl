首个独立 registryctl 版本，提供 Account 用户/机器认证、OCI layout/archive 输入、R2 分片直传、断点恢复和标准 Manifest 发布。

包含 Linux、macOS、Windows 的 amd64/arm64 二进制包、SHA256SUMS、依赖许可证与构建提交信息。可使用仓库根目录的 GitHub Composite Action 安装并推送镜像；CI 凭据从环境读取，不依赖桌面凭据库，不需要 R2 Key。

首版不接管原生 docker push，不接受 docker save archive 冒充 OCI archive。独立校验和超时策略由 Registry 服务端负责，上传完成后仍需等待可信校验。
