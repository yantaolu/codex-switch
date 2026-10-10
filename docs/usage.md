# 使用指南

## 交互菜单

运行 `codex-switch` 或 `codex-switch ui` 进入菜单。↑↓ 循环选择，Enter 确认，Esc 返回，Ctrl+C 退出；主菜单支持 Q 退出。所有二级选择菜单的「返回」位于最后。

| 菜单 | 用途 |
| --- | --- |
| 账户列表 | 查看账户详情，修改备注、登录或编辑服务配置 |
| 切换账户 | 启用指定账户，默认选中当前账户 |
| 添加账户 | 登录新官方账户或粘贴第三方 TOML |
| 模型管理 | 修改所选第三方账户的模型目录 |
| 当前状态 | 查看实际认证、订阅和额度 |
| 会话接续 | 为其他服务商的旧会话创建接续副本 |
| 切换日志 | 查看切换结果、失败阶段和回滚状态 |
| 删除账户 | 删除非当前账户，默认选中返回 |

发现未完成事务时显示「恢复配置」。切换默认当前账户，删除与会话接续默认返回，防止连续回车误操作。明确选中目标后直接执行。

## 官方账户

保存 Codex 已有登录，再添加其他账户：

```sh
codex-switch add chatgpt personal --current
codex-switch add chatgpt work
codex-switch list
codex-switch use work
```

新账户在独立临时目录使用官方登录，不覆盖当前认证。设备登录可使用 `add chatgpt work --device-auth`。同一身份和工作空间不能重复添加。

账户备注不是登录名，支持 1–64 个字符；非 ASCII 备注会生成稳定 ID。用 `list` 查看 ID，后续命令使用 ID，修改备注不改变 ID：

```sh
codex-switch remark work '工作账户'
codex-switch login work
```

`login` 重新认证原账户；所选账户不是当前账户时不会激活它。重新认证必须匹配原身份和工作空间。

切换会检查 access token 的有效期；已过期、将在 30 秒内过期或无法识别时，先使用官方 CLI 刷新，必要时登录。完整新凭据先存档，再切换。取消后不继续切换；已写出的新凭据仍受保护，重试不会被旧认证覆盖。

## 切换与强制切换

```sh
codex-switch use work
codex-switch use work --force
```

普通切换正常退出关联 APP；独立 CLI、IDE Codex 集成或 daemon 存在时提示其类型和 PID。强制切换先正常退出，再终止残留关联进程，并检查进程身份与 Codex 目录。无法确认归属、目标是工具的祖先进程或进程持续重启时停止操作。

强制切换会中断关联任务，保留其他 Codex 目录和无关应用。完成、失败和回滚均不自动启动 APP，用户手动打开客户端。

## 额度与会话

```sh
codex-switch status
codex-switch status work --refresh
codex-switch sessions --limit 100 --offset 0
codex-switch sessions <会话ID>
codex resume <新会话ID>
```

账户列表和详情每次进入查询一次，先展示缓存，R 手动刷新；不定时查询。官方查询只使用隔离进程读取额度，不后台轮换 refresh token。第三方查询配置按账户保存，见[服务商配置](providers.md)。

会话列表排除与当前服务商一致的会话，显示标题、时间、服务商和 ID。接续使用原生 `thread/fork` 创建副本，保留原会话，不发送模型请求。桌面客户端需重新打开会话列表，选择新副本。

## 备份与存储

```sh
codex-switch backup ~/accounts.cxsb
codex-switch --store ~/new-vault restore ~/accounts.cxsb
codex-switch doctor
```

密码至少 12 字节；自动化可用 `--password-stdin`，不接受命令行密码。备份支持跨机器恢复；密码丢失无法解密。导出不覆盖已有文件，恢复只接受空账户库，不覆盖当前配置、不自动激活账户。存在未完成事务时先恢复事务。

| 路径 | 内容 |
| --- | --- |
| `~/.local/share/codex-switch/vault.enc` | 加密账户库及查询配置 |
| 同目录 `usage.enc` | 加密额度缓存 |
| 同目录 `pending.enc` | 加密事务恢复记录 |
| 同目录 `catalogs/` | 生成的第三方模型目录 |
| 同目录 `switch.log` | 不含凭据的操作日志，最多保留当前和三份轮转文件 |
| `CODEX_HOME` 或 `~/.codex` | 当前认证、配置、历史及会话标题索引 |

账户目录权限为 700，凭据文件为 600。加密密钥位于 macOS Keychain；直接复制账户目录不能代替跨机器密码备份。卸载不删除数据与 Keychain。

## 命令参考

用 `codex-switch --help` 或 `<命令> --help` 查看完整用法；帮助不读取账户库。

| 命令 | 作用 |
| --- | --- |
| `init`、`list`、`version` | 初始化存档、列账户、查看版本 |
| `add chatgpt <备注> [--current] [--device-auth]` | 保存现有登录或添加官方账户 |
| `add provider <备注> [--toml-file 文件\|--file 文件]` | 编辑或导入第三方账户 |
| `use <ID> [--force]`、`remove <ID>` | 切换或删除账户 |
| `remark <ID> <备注>`、`login <ID>`、`edit <ID>` | 修改备注、官方重认证或编辑第三方 |
| `models <ID> [模型ID列表 默认ID]` | 配置所选账户的模型 |
| `quota <ID> [--file JSON]`、`status [ID] [--refresh]` | 查询设置和账户状态 |
| `backup <文件>`、`restore <文件>` | 密码备份与恢复 |
| `recover`、`doctor`、`logs` | 恢复事务、只读诊断和最近日志 |
| `sessions [--limit N] [--offset N] [会话ID]` | 列出候选或创建接续副本 |

全局参数置于命令前：`--home`、`--store`、`--app`、`--codex`。每个账户库绑定一个 Codex 目录；不同目录使用独立 `--store`。旧参数 `--no-restart` 保持兼容，切换始终不启动客户端。
