# 开发指南

## 本地环境

使用 macOS、Python 3 和满足 `go.mod` 的 Go 版本。原生集成需要官方 Codex CLI；Homebrew 验收需要 `brew`。从仓库根目录运行：

```sh
make build VERSION=1.0.1
bin/codex-switch version
go test -race -count=1 -timeout 3m ./...
go vet ./...
go mod verify
```

`make build` 默认版本与当前源码版本一致；发布构建始终显式传入版本。依赖更新同步维护 `go.mod` 与 `go.sum`，不要提交 `vendor`、`bin` 或 `dist`。

## 验证范围

普通测试覆盖配置幂等、导入边界、存档与备份、故障注入、认证轮换、事务恢复、进程筛选、会话标题及交互取消。涉及原生 Codex 的测试在程序可用时验证协议；程序缺失可能跳过，测试通过不代表完成所有客户端集成验收。

受控进程验收为显式启用，不应对真实工作任务使用测试命令：

```sh
CXS_PROCESS_ACCEPTANCE=1 go test ./internal/switcher \
  -run '^TestControlledForceStopKeepsOtherHome$' -count=1 -timeout 30s
```

漏洞检查与发行包构建：

```sh
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
python3 scripts/release.py --version 1.0.1
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 \
  -mode=binary dist/darwin-arm64/codex-switch
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 \
  -mode=binary dist/darwin-amd64/codex-switch
```

## Homebrew 与启动性能

```sh
python3 scripts/brew-acceptance.py
mkdir -p dist/validation
python3 scripts/measure-startup.py bin/codex-switch \
  --output dist/validation/startup.json
```

Homebrew 脚本在临时 Tap 中验证安装、升级、卸载及数据保留，默认报告写入 `dist/validation/brew.json`。`--use-installed-go` 可在开发环境复用现有 Go；该结果不代替标准构建依赖安装的验收。

启动测量只调用不读取账户库的 `version` 与 `--help`，不切换账户、不访问模型 API。性能报告随环境变化，保存测量条件，不把单机结果当成通用承诺。

## CI 与文档

CI 在 Apple Silicon 与 Intel macOS runner 上执行测试、静态检查、漏洞检查、受控进程验收、Homebrew 验收和打包。手动候选工作流只生成附件；稳定 tag 触发正式工作流，两个架构各自测试和运行程序，全部通过后创建 Release 草稿。公开 Tap 使用独立 bottle 验收与发布流程。详见[发布指南](releasing.md)。

`docs` 仅保留对外维护的 Markdown：使用、服务商、排错、架构、开发和发布。HTML 审查报告、操作日志、复现材料与性能数据写入 `dist/validation` 或仓库外；Git 忽略 IDE 和 Python 缓存。

## 修改约定

- 保持子命令与 TUI 使用同一引擎，涉及写入的操作走现有锁和事务。
- 进程判定不扩大终止范围；取消要等待清理，提交后警告不能触发错误回滚。
- 避免网络请求、完整会话扫描或大文件读取阻塞界面更新。
- 为有实际风险的行为添加回归验证，使用隔离临时目录和测试凭据。
- 更新用户可见行为时同步 README、相关文档与版本记录。

提交问题时提供版本、macOS/架构、原生 Codex 版本、操作步骤和脱敏提示；提交代码时说明行为变化及验证范围。禁止包含真实 token、密钥、账户库或个人会话。
