# codex-switch

一个轻量的 macOS 命令行工具，用于切换 ChatGPT 官方账户和 OpenAI Responses 兼容服务商。提供键盘交互菜单与子命令，桌面客户端和 CLI 共用同一套配置。

项目地址：https://github.com/yantaolu/codex-switch

## 功能

- 管理多个官方账户，凭据过期时通过官方 Codex 刷新或重新登录。
- 粘贴 TOML 添加第三方服务商，按账户配置接口、密钥、模型和额度查询。
- 切换前退出关联客户端，支持显式强制切换；完成后手动打开 APP 或 CLI。
- 保留无关配置与历史，通过原生会话副本接续其他服务商的会话。
- 加密账户存档，密钥保存在 macOS Keychain；支持密码备份、恢复和切换日志。
- 单一 Go 可执行文件，无常驻服务、代理或 WebView。

## 环境与安装

运行平台为 macOS。官方登录、切换核验、官方额度查询和会话接续需要官方 Codex CLI；桌面支持包含 Codex 组件的 ChatGPT.app 或 Codex.app。

通过 Homebrew 安装：

```sh
brew install yantaolu/tap/codex-switch
```

Tap 提供 Apple Silicon 和 Intel 的预编译 bottle；匹配平台时无需安装 Go。升级使用 `brew upgrade codex-switch`，卸载保留账户数据。

从源码安装需要 Go 1.26.9 或更新版本：

```sh
git clone https://github.com/yantaolu/codex-switch.git
cd codex-switch
make test
make install VERSION=1.0.1
```

安装路径为 `~/.local/bin/codex-switch`，请确保该目录在 `PATH` 中。运行 `codex-switch version` 核对版本。

手动安装可下载[项目 Releases](https://github.com/yantaolu/codex-switch/releases) 中的 `darwin_arm64`（Apple Silicon）或 `darwin_amd64`（Intel）压缩包，核对校验和后把可执行文件放入 `PATH`。Homebrew 配方见[公开 Tap](https://github.com/yantaolu/homebrew-tap)。

## 开始使用

直接运行进入交互菜单：

```sh
codex-switch
```

使用 ↑↓ 首尾循环选择、Enter 确认、Esc 返回、Ctrl+C 退出。蓝色为标题，绿色为当前账户或选中项，选中标记为 `>`；支持 `NO_COLOR`。

若已经在 Codex 登录官方账户，可以先保存现有登录：

```sh
codex-switch add chatgpt personal --current
codex-switch add chatgpt work
codex-switch list
codex-switch use work
```

账户备注用于本地区分账户。`list` 显示稳定 ID，后续命令使用 ID；备注可修改，ID 保持不变。切换完成后手动打开客户端。

第三方账户可在菜单「添加账户 → 第三方配置」粘贴 TOML，再按 Ctrl+S 保存；也可从文件导入：

```sh
codex-switch add provider relay --toml-file provider.toml
codex-switch use relay
```

导入只保存账户；`use` 才会启用。第三方接口需要支持 Responses API，模型及图片请求能否成功取决于客户端与服务商。

## 文档

| 文档 | 内容 |
| --- | --- |
| [使用指南](docs/usage.md) | 官方账户、交互菜单、子命令、备份与存储 |
| [服务商配置](docs/providers.md) | TOML 导入、模型目录、图片授权和额度模板 |
| [故障排查](docs/troubleshooting.md) | 进程阻塞、凭据失效、中断恢复和会话接续 |
| [架构说明](docs/architecture.md) | 模块、事务、凭据与资源边界 |
| [开发指南](docs/development.md) | 构建、测试、CI 和贡献约定 |
| [发布指南](docs/releasing.md) | 打包、验收、Release 与 Homebrew |
| [版本记录](CHANGELOG.md) | 对外版本变化 |

## 数据与行为

默认账户库为 `~/.local/share/codex-switch`，Codex 目录为 `CODEX_HOME` 或 `~/.codex`。每个账户库绑定一个 Codex 目录。

账户存档加密，当前生效的 `auth.json` 与部分第三方 `config.toml` 仍包含凭据。卸载保留账户库与 Keychain；跨机器迁移使用密码备份。

普通切换会退出关联桌面客户端；存在独立 CLI、IDE 集成或 daemon 时会提示先退出。`use <ID> --force` 明确授权终止关联 Codex 任务，随后切换；切换和回滚均不自动启动客户端。

本地核验不代表上游模型可用；不同服务商也不一定支持相同的历史工具、图片或推理记录。详情见[故障排查](docs/troubleshooting.md)。

## 许可证

[MIT](LICENSE)。
