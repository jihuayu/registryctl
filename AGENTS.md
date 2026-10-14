# registryctl 开发约定

这是独立公开的 CLI 与 GitHub Action 仓库，只维护客户端、OCI 输入和 Account HTTP 认证。服务端在 home-service/apps/registry 维护；本仓库不得依赖私有 Git 仓库、Distribution 补丁缓存、生产数据库或 R2 通用 Key。

文档、提交、PR 和协作记录使用中文。使用 codex/ 分支开发，通过 PR squash 合并 main，不绕过保护。Go 依赖保持独立模块与锁文件；发布前运行 go test ./...、go vet ./...、安装脚本和跨平台打包检查。认证/上传行为改动须验证独立模拟上游，不用生产数据库做测试。

版本从 VERSION 开始，正式发布使用 vMAJOR.MINOR.PATCH 标签，GitHub Actions 从标签准确提交构建、校验、打包并发布 Release。v1 是供 Action 使用的主版本别名，只能由已经验收的正式版本向前更新。公开 Release 不覆盖已有附件。

凭据、Token、签名 URL、生产配置和原始日志不得提交或输出。GitHub Actions 优先使用绑定 Account 用户的 OIDC 和精确 workflow 策略；兼容机器客户端时通过 repository/environment secrets 提供 Account 客户端凭据，不能要求 R2 通用凭据，也不能在不可信 PR 中使用生产 Secret。
