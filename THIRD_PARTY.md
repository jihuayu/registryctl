# 第三方组件

本仓库自己的客户端代码使用 MIT License。Go 依赖保持各自许可证，具体版本见 go.mod/go.sum：

| 模块 | 许可证 |
| --- | --- |
| github.com/distribution/reference | Apache-2.0 |
| github.com/opencontainers/go-digest | Apache-2.0 |
| github.com/opencontainers/image-spec | Apache-2.0 |
| github.com/zalando/go-keyring | MIT |
| github.com/danieljoos/wincred | MIT |
| github.com/godbus/dbus/v5 | BSD-2-Clause |
| al.essio.dev/pkg/shellescape | MIT |
| golang.org/x/sys | BSD-3-Clause |

发布包同时包含这些依赖的实际许可证文本，保存在 licenses 目录。客户端最初从自有 home-service 的固定提交拆出，文件来源哈希记录在 source.json。未复制 Distribution 服务端源码；直接依赖的 reference 是独立项目。
