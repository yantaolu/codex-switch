# 发布指南

## 1.0.0 发布范围

发布 macOS Apple Silicon、Intel 的可执行包，以及供 Homebrew 离线构建的 vendored 源码包。附件包含许可证、公开文档和依赖信息。

稳定 tag 触发正式工作流，在双架构验收通过后创建 GitHub Release 草稿。维护者核对后公开 Release，再通过 Tap 的 PR 验收和发布 bottles。候选工作流仍只生成附件。

## 发布前检查

1. 检查 Git 工作区、变更说明及文档，不带入密钥、账户备注、机器路径或开发报告。
2. 完成[开发指南](development.md)中的测试、静态与漏洞检查，确认双架构 CI 成功，核对未执行或跳过的集成项。
3. 在实际客户端验证官方与第三方切换、过期重认证、取消、强制退出、事务恢复、额度、会话接续及备份恢复；使用测试账户。
4. 验证两种架构运行，以及标准 Homebrew 安装、升级、卸载；单机或复用现有 Go 的结果不能替代缺失项。
5. 更新 [CHANGELOG](../CHANGELOG.md) 的发布状态与日期，记录实际验收的 macOS 和 Codex 版本，不承诺未测版本。

保存原始验收材料到 `dist/validation` 或 CI 附件，不提交到 `docs`。候选确认后不再修改源码；有修改就重新构建相关产物并验证。

## 构建与核对附件

从将要发布的提交构建，使用独立输出目录，避免混入历史版本：

```sh
python3 scripts/release.py --version 1.0.0 --output dist/1.0.0
(cd dist/1.0.0 && shasum -a 256 -c checksums.txt)
dist/1.0.0/darwin-arm64/codex-switch version
tar -tzf dist/1.0.0/codex-switch_1.0.0_source.tar.gz
```

Apple Silicon 本机运行 arm64；Intel 本机运行 amd64。使用与本机匹配的二进制核对输出为 `1.0.0`。

| 附件 | 用途 |
| --- | --- |
| `codex-switch_1.0.0_darwin_arm64.tar.gz` | Apple Silicon |
| `codex-switch_1.0.0_darwin_amd64.tar.gz` | Intel |
| `codex-switch_1.0.0_source.tar.gz` | 包含依赖的源代码 |
| `checksums.txt` | 三份归档的 SHA-256 |
| `codex-switch.rb` | 绑定版本 URL 与源码校验和的 Homebrew Formula |

脚本规范化归档时间及所有者；二进制构建仍需同一源码、Go 工具链与依赖才能比较重现性。不要上传整个 `dist`，其中包含构建中间文件。

## GitHub Release

将审核过的代码与文档提交到仓库，在相同提交创建并推送 `v1.0.0` tag。`.github/workflows/release.yml` 在 Apple Silicon 和 Intel macOS runner 上分别执行测试、原生 Codex 协议、受控进程、Homebrew 生命周期及漏洞验收，再构建并运行各自的安装包。

两个架构通过后，工作流核对各自校验和与源码归档一致性，合并附件并创建草稿。重试只更新现有草稿，拒绝覆盖已公开版本。也可手动运行正式工作流，输入已有稳定 tag。

草稿附上表中的五个文件，初始说明来自 CHANGELOG。维护者补充安装方式、兼容性验收、升级注意事项，以及「切换后手动打开客户端」。源码与 tag 必须对应。

核对后发布草稿。上传 tarball 的 HTTPS 地址必须与生成 Formula 中的 `url` 一致；附件更换后同步校验和与 Formula，不在版本不变时悄悄替换二进制。

当前流程没有配置 Developer ID 签名或公证。不要在发布说明中声称已签名、公证；需要该能力时另行加入构建及验收流程。

## Homebrew Tap

Tap 位于 `https://github.com/yantaolu/homebrew-tap`。通过 PR 更新 `Formula/codex-switch.rb` 中的版本、ARM64 与 AMD64 安装包 URL 和 SHA-256，确认其对应已经公开的二进制附件。Release 中生成的 `codex-switch.rb` 是可选的源码构建配方，与 Tap 的二进制安装配方分别维护。

Tap 的 `tests.yml` 使用 Homebrew 官方 test-bot 在双架构 macOS runner 上验证配方、构建和测试 bottles。全部检查通过后，运行 `publish.yml`，填写 PR 编号及已审核的完整 head SHA；工作流核对 PR 状态与检查结果，使用 `brew pr-pull` 发布 bottles 并提交相应校验和。

匹配平台安装时自动使用 bottle；其他 macOS 平台安装对应架构的正式版二进制包，均无需 Go。Tap 从已通过原生验收的二进制附件生成 bottles，安装相同的文档与许可证。bottle 是 Homebrew 专用归档，与主 Release 的手动安装包分别维护。

自行选择源码构建配方时，其 Go 依赖仅用于构建，源码归档中的 `vendor` 提供依赖，构建关闭依赖网络下载。Homebrew 在部分 Intel 平台没有 Go 的预编译包，源码构建可能需要自行准备工具链；日常安装推荐使用 Tap。

```sh
brew install yantaolu/tap/codex-switch
brew upgrade codex-switch
```

公开 Tap 后，验证真实 URL 下载、bottle 安装、版本输出、升级和卸载数据保留，再完成发布验收。主项目工作流只需本仓库 `GITHUB_TOKEN`；Tap 在自己的工作流中发布 bottles，不需要主项目持有跨仓库写入密钥。

卸载保留账户库和 Keychain。旧凭据助手路径失效时先运行 `doctor`，再重新启用对应账户以写入稳定路径，不删除用户数据。

## 后续版本

发布后以新版本修复问题，保留旧 Release 和校验和。依赖、Codex 原生协议、配置字段或进程识别发生变化时重新验证相关边界。账户格式调整必须提供迁移测试，不能通过删除旧账户数据完成升级。
