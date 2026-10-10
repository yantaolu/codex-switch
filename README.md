# codex-switch

在 macOS 上管理多个 ChatGPT 官方账户和 OpenAI Responses 兼容服务商。所有账户共用一个 Codex 工作目录；确认关联进程退出后切换认证与模型配置，由用户手动打开 APP 或 CLI。

## 功能

- 官方账户首次登录一次，随后按名称切换；登录新账户使用独立临时目录。
- 键盘菜单管理账户，无需记忆子命令。
- 第三方配置统一管理接口地址、API key、模型列表、默认模型及额外请求头，不绑定特定服务商。
- 切走前保存当前官方账户的最新认证，保留 MCP、hooks、插件、项目设置和历史文件。
- 退出 APP、替换认证与配置、检查本地认证；不自动启动 APP。
- 切换失败回滚、中断操作恢复和外部写入冲突检测。
- 使用 AES-256-GCM 加密账户与恢复记录；加密密钥保存在 macOS Keychain。

## 环境

macOS。登录、切换、官方额度查询与会话接续需要官方 Codex CLI；离线账户存档管理无需提前找到 CLI。桌面支持包含 Codex 组件的 ChatGPT.app 或 Codex.app。源码构建需要 Go 1.26.9 或更新版本；Intel 和较旧 macOS 的正式支持以 CI 与真机验收为准。

第三方接口必须支持 Responses API。仅支持 Chat Completions 的接口不能直接使用。第一版不包含协议转换代理。

## 安装

```sh
make test
make build
make install
```

可执行文件安装到 `~/.local/bin/codex-switch`。确保该目录已在终端 `PATH` 中。安装不自动更改登录或 Codex 配置。

## 开始使用

直接运行：

```sh
codex-switch
```

主菜单显示完整项目地址 `https://github.com/yantaolu/codex-switch`。标题和地址使用 OSC 8 超链接明确指向该项目；不支持超链接的终端仍可根据完整网址识别链接。终端是否需要按住 Command 点击由终端设置决定。

使用 ↑↓ 首尾循环选择、Enter 确认、Esc 返回、Ctrl+C 退出。菜单平铺为「账户列表」「切换账户」「添加账户」「模型管理」「当前状态」「删除账户」「退出」。仅检测到未完成的切换时显示「恢复配置」。账户列表只读，选中账户查看详情不会切换。添加菜单包括「官方登录」「第三方配置」。账户备注用于本地区分账户，不是登录名；已有备注和命令参数保持兼容。新增账户支持中文备注，自动分配稳定 ID；`list` 显示 ID，切换等命令使用 ID。旧的 5X、20X 等 ID 保持兼容。账户详情可修改备注、重新登录官方账户或编辑第三方服务配置。界面标题使用亮蓝色，当前账户和选中项使用黄绿色；选中项使用 `>` 标记，不使用背景高亮。菜单与详情左对齐，编辑框不使用外边框；成功和错误分别使用黄绿色和柔和红色。支持 `NO_COLOR` 关闭配色。

选择「模型管理」后先选择一个第三方账户，再新增、移除模型及选择默认模型；仅修改所选账户，不影响其他账户。保存当前账户的模型修改会先退出 APP，完成后由用户手动打开；修改未启用账户只更新存档，下次切换时生效。取消修改不会保存草稿。模型列表最多 200 项，默认模型必须在列表中。

手动模型列表会生成 Codex 可读取的本地模型目录，供 CLI 和桌面 APP 选择；官方账户继续使用官方模型目录。导入包含 `model_catalog_url` 的账户可以使用服务商的远程模型发现，保留返回的模型能力信息。模型 ID 必须与服务商一致，列入列表不代表该模型可用。

交互和二级命令同时提供。`codex-switch ui` 明确打开交互菜单；`codex-switch --help`、`codex-switch help` 和各子命令后的 `--help` 正常显示帮助，不读取账户凭据。

```sh
codex-switch list                  # 查看全部账户，* 表示当前账户
codex-switch status                # 检查实际认证与当前记录
codex-switch use 5X                # 直接切换到指定账户
codex-switch models relay          # 交互编辑指定第三方账户模型
codex-switch add provider --help   # 查看添加命令用法
```

官方账户切换会检查 access token 的到期时间。已过期、将在 30 秒内过期或无法识别到期时间时，先在独立临时目录使用官方 Codex 刷新认证；刷新无法完成时打开官方浏览器登录。请登录原账户及工作空间。工具验证完整认证和账户身份一致后，先保存最新凭据到加密存档，再继续切换，完成后不自动启动 APP。登录取消、登录错账户或结果仍过期时不切换；切换后续步骤失败时，目标账户的新凭据仍保留供重试。有效凭据不触发重新认证，第三方 API Key 不走此流程。

刷新或登录即使返回中断错误，也会检查已经写出的凭据。同一账户和工作空间的新凭据会先存档；用户取消后不继续切换或浏览器登录。存档记录未同步到当前认证的状态，避免下次切换将旧凭据覆盖回来。编辑器保存期间支持 Esc 和 Ctrl+C，取消后等待保存任务和事务清理；已经提交的修改会明确提示。

账户切换始终指定名称，不提供“上一账户”快捷切换。提交之前的写入或认证核验失败会自动回滚；切换、失败回滚和模型修改均不会自动启动 APP。交互菜单使用安全默认项避免连击：切换列表默认当前账户，选中当前账户直接返回，不退出进程、不更新认证；删除和会话接续默认返回。明确选择其他账户后直接执行，不再叠加确认页。失败默认返回；待恢复或结果未确认时不提供盲目重试。

以下命令也适用于脚本：

先保存正在使用的官方账户：

```sh
codex-switch add chatgpt personal --current
```

添加另一个官方账户，在浏览器中选择对应账户和工作空间：

```sh
codex-switch add chatgpt work
# 或使用设备登录
codex-switch add chatgpt work --device-auth
```

添加第三方配置：

```sh
codex-switch add provider relay
```

命令打开同页 TOML 编辑框，并预填账户备注。粘贴配置后可继续修改，按 `Ctrl+S` 保存。服务商名称由您指定。

查看与切换：

```sh
codex-switch list
codex-switch use work
codex-switch use relay
codex-switch status
```

切换始终不自动启动 APP。完成后手动打开桌面客户端或启动 CLI。普通切换会正常退出运行中的 APP；独立 CLI、IDE Codex 集成或 daemon 存在时，明确列出进程类型和 PID，等待您退出或使用强制切换。关闭 IDE 的终端标签不一定结束其 Codex 集成。旧脚本的 `--no-restart` 参数仍接受，但不再改变行为。

## 强制切换

普通切换因 Codex 进程阻塞时，失败页提供「重试切换」「强制切换」「返回」，默认选中返回。选择强制切换时同页说明会中断关联任务，不再增加确认页；默认返回可以防止连续回车终止进程。

```sh
codex-switch use work --force
```

命令中的 `--force` 明确授权结束关联进程，不额外弹出交互确认。强制切换先尝试正常退出桌面客户端，并通过官方命令停止 daemon；随后向残留 Codex CLI、IDE ACP 集成、专属包装进程和子进程发送正常终止信号，等待后仅对仍存活的目标强制终止。按 `CODEX_HOME` 区分 CLI 和桌面进程，保留使用其他目录的客户端；无法确定目录时阻止强制终止。每次发送信号前核对 PID、启动时间、可执行文件和用户身份。认证等待后、正式写入前再次检查进程。必须确认关联进程全部退出，才会继续认证、保存凭据及切换；发现进程持续被重启时停止操作。

强制切换会中断运行中的 Codex 任务。保留 WebStorm、终端应用、CC Switch 和其他客户端；共享 Node 进程不作为专属包装进程关闭。请从独立终端运行，工具会拒绝终止启动它的 Codex 会话及其祖先进程。所有关联进程确认退出后才切换账户；完成或失败均不自动启动任何 APP 或会话。

## 导入第三方配置

快速导入 CC Switch/Codex TOML：

1. 复制完整 TOML 配置。
2. 运行 `codex-switch`，选择「添加账户」→「第三方配置」。
3. 在同一页面粘贴 TOML 并填写账户备注。配置默认明文显示，支持方向键移动、修改、删除和多行编辑；`Tab` 切换名称与配置字段。
4. 按 `Ctrl+S` 自动解析并保存，不需要单独解析或摘要确认。格式错误、名称重复或保存失败会在页面内提示并保留编辑内容；`Esc` 放弃返回。保存只添加账户，不切换当前登录；之后从「切换账户」启用。

也可以从文件导入：

```sh
codex-switch add provider relay --toml-file /absolute/path/config.toml
```

导入根据 `model_provider` 选择对应 provider，支持 `base_url`、`experimental_bearer_token`、`wire_api = "responses"`、`requires_openai_auth = false`、`http_headers` 和 `model_catalog_url`。图片执行器使用的 `x-openai-actor-authorization` 请求头按原值保留。支持的 provider 重试、流超时、WebSocket 开关也会保存。外部认证命令和环境变量凭据不自动导入，请先改为 bearer token。

`review_model`、`disable_response_storage`、`network_access`、`windows_wsl_setup_acknowledged` 及 `features.api_key_model_discovery`、`features.goals` 随账户保存和切换；切回官方账户恢复该账户的设置。三个旧字段 `disable_response_storage`、`network_access`、`windows_wsl_setup_acknowledged` 未被当前 Codex schema 声明，保留但可能被忽略。其他未知字段明确提示未导入；不导入 MCP、hooks、外部命令或本地文件路径。

API 和远程模型目录支持 HTTP/HTTPS 地址及查询参数，允许使用不同域名；不接受 URL 内嵌认证和片段。远程目录需要返回当前 Codex 可识别的模型目录格式；普通 `/models` 列表不一定兼容。`api_key_model_discovery` 必须启用才能发现远程模型。菜单「模型管理」可以切换到手动列表，也可恢复已保存的远程目录。

导入保留直接 bearer 认证，避免认证命令与该字段冲突。密钥在账户存档中加密；启用账户时写入权限为 600 的 `config.toml` 和 `auth.json`，以保留 API Key Mode 和客户端兼容配置。请将这两个文件视为凭据。

图片执行器授权配置可以保留，但实际生图取决于客户端版本、扩展和服务商支持。导入成功或模型发现成功均不代表已经完成生图验收。

可从本地 JSON 文件添加：

```sh
codex-switch add provider relay --file /absolute/path/provider.json
```

文件格式：

```json
{
  "base_url": "https://gateway.example/v1",
  "model": "your-model-id",
  "models": [
    { "id": "your-model-id", "name": "主力模型", "context_window": 128000 },
    { "id": "another-model-id" }
  ],
  "api_key": "your-api-key",
  "headers": { "X-App-Name": "codex-switch" }
}
```

地址支持 HTTP/HTTPS 和查询参数，不接受 URL 内嵌认证或片段。Authorization 由认证命令提供，不能作为自定义请求头。

导入文件最多 1 MiB，仅接受普通文件，拒绝符号链接和命名管道；TOML、provider JSON 与额度查询 JSON 均采用有界读取。输入文件包含密钥，应限制为本人可读并在导入后妥善处理。不要提交到仓库。

`model` 表示默认模型；`models` 是模型列表，`name` 和 `context_window` 可省略。旧配置只有 `model` 时自动作为单模型列表。使用 `codex-switch edit <ID>` 或账户详情「服务编辑」修改地址、API Key、请求头和远程目录；保留单独管理的模型列表和稳定 ID。修改当前账户通过切换事务生效，修改其他账户只更新存档。加密存档不支持直接编辑。已添加账户的模型列表建议通过菜单修改，也可使用 `codex-switch models relay 'model-a,model-b' model-a`。

## 订阅与额度

账户列表和详情显示官方订阅额度、Sub2API 余额与订阅，或通用模板查询结果。每次进入列表或详情仅查询一次，先显示缓存，后台更新结果；不定时刷新，不显示改变标题布局的加载提示，按 `R` 可手动刷新。查询不会切换账户或重启 APP。失败显示原因及上次成功时间，不将失败当作余额为零。离开页面会取消该页查询。

官方账户自动启用查询，使用已保存的登录；当前账户优先读取身份一致的实时认证。显示接口返回的套餐、额度周期、剩余百分比、重置时间和可选 credits。credits 不表示现金余额；接口未返回的周期不会虚构。查询通过隔离的官方 Codex app-server 进程使用 access token，不传入 refresh token，不覆盖现有认证。登录失效时明确提示，不后台轮换或刷新登录。

第三方查询配置**按账户保存**。打开「账户列表」→ 选择账户 →「额度配置」，可选择：

- **Sub2API**：填写或确认 `/v1/usage` 查询地址，默认复用账户 API Key。根据服务端返回的钱包、订阅、Key 配额模式展示余额、日/周/月额度、到期和窗口重置时间。服务端未返回的信息不作推断。
- **通用模板**：在明文 JSON 编辑框粘贴或修改查询地址、请求头、字段路径及换算系数，`Ctrl+S` 保存，`Esc` 放弃。
- **关闭查询**：仅关闭所选第三方账户的额度查询。

通用模板支持 GET/POST，使用 `{{apiKey}}` 引用账户 API Key、`{{baseUrl}}` 引用模型接口地址（用于请求头和请求体）。可设置独立 `api_key` 作为查询凭据；`headers` 可以覆盖 Authorization 或设置其他鉴权头。模型接口的图片执行器请求头不自动附加到额度查询。

字段路径支持 `data.balance`、`data.items.0.balance` 或 JSON Pointer `/data/balance`。可映射 `remaining`、`used`、`total`、`plan`、`unit`、`expires_at`、`is_valid`；`scale` 仅换算数值，省略时为 1。到期时间支持 RFC3339 字符串或 Unix 秒。通用模板采用 HTTP 请求与字段映射，不执行 JavaScript，也不直接导入 CC Switch 的 JavaScript 脚本。

```json
{
  "type": "generic",
  "url": "https://gateway.example/api/user/self",
  "headers": { "Authorization": "Bearer {{apiKey}}" },
  "fields": {
    "remaining": "data.quota",
    "used": "data.used_quota"
  },
  "scale": 0.000002,
  "unit": "USD"
}
```

也可使用二级命令：

```sh
codex-switch status                     # 查询当前账户；30 秒内使用缓存
codex-switch status 20X --refresh       # 查询指定账户，不切换账户
codex-switch quota relay                # 交互配置第三方额度查询
codex-switch quota relay --file usage.json
```

Sub2API 文件配置可简化为 `{"type":"sub2api"}`，查询地址根据模型接口补全；特殊部署可通过 `url` 指定完整地址。需要独立查询密钥时增加 `api_key`。未配置查询的第三方账户不会自动发起额度请求。

查询设置与凭据一起保存在加密账户存档 `vault.enc`，查询缓存保存在同目录的 `usage.enc`，同样加密。查询配置仅由 codex-switch 使用，不写入 Codex 的模型配置。旧账户存档无需迁移。

## 存储与配置

默认 Codex 目录是 `CODEX_HOME`，未设置时为 `~/.codex`。默认账户目录为 `~/.local/share/codex-switch`。

```sh
codex-switch --home /path/to/codex --store /path/to/accounts list
codex-switch --app /Applications/ChatGPT.app use work
codex-switch --codex /absolute/path/codex use work
```

全局选项置于命令前；子命令选项置于对应子命令和账户备注后。每个账户存储绑定一个 Codex 目录。不要混用目录，也不要在配置引用它后随意移动或删除可执行文件。

账户存档和恢复记录为加密文件；目录权限为 700，文件权限为 600。当前生效的认证仍按 Codex 的文件存储格式写入 `auth.json`，应视作密码保管。

第三方模型目录位于账户目录下的 `catalogs/`，不含密钥，按内容生成文件名；请通过菜单修改模型列表，不直接改生成文件。旧目录文件保留供事务回滚使用。

手动添加的第三方账户默认不把密钥写入 `config.toml`。工具创建 `model_providers.codex_switch`，通过内部 `credential` 命令提供 bearer token。该命令供 Codex 调用，会输出密钥，不应手动执行或收集其输出。TOML 导入的账户保留直接 bearer 模式，会在启用时写入密钥。

切换保留无关配置及其他 provider 定义。工具管理模型、provider 选择、登录限制、认证存储方式及相关地址、模型目录字段。官方账户保留各自模型设置，官方模式移除接口地址覆盖。存在全局 `profile` 覆盖时拒绝切换，需先处理该覆盖。

账户功能开关通过 `[features]` 表局部更新，保留其他开关和注释。当前生效配置使用内联或点号形式的 `features` 时，会明确拒绝切换；请改为 `[features]` 表后重试。

## 恢复

```sh
codex-switch recover  # 处理崩溃或中断留下的恢复记录
```

`recover` 会保留已提交的切换，或恢复未提交操作的配置与认证。遇到外部文件修改时拒绝覆盖，避免丢失其他程序的改动。恢复后手动重新打开 APP。

当前账户不能删除。其他账户可通过「删除账户」或 `codex-switch remove <名称>` 删除。

## 会话接续

切换账户后，在交互菜单选择「会话接续」，选择旧会话并确认。工具使用 Codex 原生 `thread/fork` 接口保留历史、创建使用当前账户的接续副本；原会话保留，不会批量改写历史记录，也不会在复制时发送模型请求。

```sh
codex-switch sessions                # 列出本地会话
codex-switch sessions <会话ID>       # 使用当前账户创建接续副本
codex resume <返回的新会话ID>        # 在 CLI 中继续
```

桌面客户端需重新打开会话列表并选择新副本。直接打开旧会话仍可能使用其原服务商；全局账户切换不会自动重绑定全部旧会话。第三方对历史推理、工具和图像记录的兼容性取决于服务商。云端会话不在此功能范围内。

会话接续的列表、执行中和结果页均支持 Esc 返回；执行中会取消请求并等待临时进程清理，Ctrl+C 退出。若取消前复制已经完成，仍显示新副本；若复制请求已发出但结果无法确认，会提示先查看会话列表，避免重复复制。

工具保持单一 Go 可执行文件，不运行常驻后台服务。原生会话接口只在接续操作时启动临时进程，完成或取消后退出；菜单没有定时网络请求。

## 切换日志与配置

交互菜单「切换日志」或 `codex-switch logs` 查看最近操作。日志默认位于 `~/.local/share/codex-switch/switch.log`，跟随 `--store`；使用 JSON Lines 格式，文件权限为 `0600`。记录时间、账户备注、操作结果、失败阶段及回滚状态，不记录密钥、认证内容、请求头或原始错误响应。旧版本执行的操作不会补录。

切换配置按 TOML 表达式处理，清理被替换字段遗留的空行，保留注释和多行字符串。第三方导入支持按账户保存 `features.image_generation`，切到其他账户时恢复其功能配置；未显式设置时沿用 Codex 默认值。图片授权 Header、Bearer 密钥和远程模型目录继续保留。

生图工具请求的模型独立于文本模型。若服务商返回 `model_not_found` 或分组内没有可用图片模型，需要服务商配置相应模型路由；账户切换工具无法修复上游分组权限。

## 第一版边界

- `status` 可检查本地身份并查询在线额度；切换验证仍只检查 Codex 能否读取认证，不承诺服务商模型可用。
- 切换不自动启动 APP；本地核验通过不等同于在线身份验证或完成模型请求。
- token 被撤销、失效或刷新失败时，仍需重新认证。
- 工具检测并发操作及部分外部写入，其他程序不参与本工具的锁；使用期间不要让其他配置切换工具改写同一目录。
- 共享本地历史文件不保证不同账户都能访问原账户的云端任务、连接器或插件服务。
- 普通切换不强制终止任务；显式强制切换会终止 Codex 关联任务。不自动轮换账户、不卸载其他工具。
- 目前没有发布签名安装包；真实多账户、Keychain 和第三方在线请求需在发布前完成实际环境验收。

## 开发

```sh
make test     # 隔离目录测试，含 race 检查
make check    # 静态检查
make build
```

测试使用内存密钥、模拟 APP 控制和临时目录，不访问真实 Keychain，不修改现有 Codex 登录。

本机安装了 Codex 时，可额外验证其能读取隔离生成的第三方认证配置，并通过原生 `model/list` 检查自定义模型目录：

```sh
CODEX_SWITCH_NATIVE_CLI="$(command -v codex)" go test -race ./...
```

该测试使用虚拟 API key，访问本地模拟模型目录，检查 bearer 与图片扩展请求头，不发起真实模型或图片请求。运行环境需允许监听 localhost。

## 许可证

MIT。

## 备份、迁移与重新认证

```sh
codex-switch remark 5X '个人账户'       # 修改备注，不改变 ID
codex-switch login 5X                  # 原账户重新认证，不激活其他账户
codex-switch backup ~/accounts.cxsb    # 输入至少 12 字节的备份密码
codex-switch --store ~/new-vault restore ~/accounts.cxsb
codex-switch doctor                    # 核查版本、存档与认证助手
```

备份使用独立密码、随机 salt、PBKDF2-HMAC-SHA256（600000 次）和 AES-256-GCM，可跨目录或机器恢复。自动化可使用 `--password-stdin`；不接受命令行密码。恢复只写入空账户库，以新目录的 Keychain 密钥重新加密，不覆盖正在使用的配置，也不自动激活账户。备份密码丢失无法恢复。导出和恢复均拒绝未完成切换；备份文件不覆盖已有文件。

v1 存档在读取时兼容为 v2；首次保存升级前保留原始加密 `vault.v1.enc`，使用原目录的同一个 Keychain 密钥。该文件是本机迁移回退副本，恢复时应在停止操作后复制回 `vault.enc`；跨机器恢复请使用密码备份。卸载不删除账户库或 Keychain。

凭据助手优先写入经同文件校验的 Homebrew `opt` 路径，升级清理旧 Cellar 后继续可用。旧配置固化 Cellar 路径时，运行 `doctor` 核查，再在退出客户端后运行 `use <当前ID>` 重写配置；不会后台改动当前账户。

`~/.local/share/codex-switch/` 保存加密账户库、加密额度缓存、待恢复记录、模型目录和操作日志；`~/.codex/` 保存当前生效的认证、配置及原会话。日志按约 1 MiB 轮转，保留当前文件和 3 份旧文件，最近记录读取有界。提交后仅回收无账户、基线或当前配置引用的模型目录；待恢复期间不回收。

会话列表排除供应商与当前一致的会话，界面每页 20 条；命令默认 100 条，可用 `sessions --limit 100 --offset 100` 分页。列表附带本地标题、时间、供应商和短 ID，命令行保留完整 ID。标题只读 `session_index.jsonl`，缺失时显示“未命名会话”；按页保留标题并限制扫描，不读取完整对话或新增数据库依赖。读取元数据保留有界结果，扫描超过 10 秒提示结果不完整。接续创建原生副本，保留原会话；服务端明确拒绝可重试，断开或异常响应提示结果未确认。不同账户之间的同供应商旧会话可直接打开，但依赖服务端状态的请求仍受上游兼容性限制。

余额只用于观察显示，保留币种、百分比与 credits 的区别，不承担账单精确结算；查询失败保留旧数据和最后成功时间。

## 发布准备

```sh
make release VERSION=0.8.0-rc.1
python3 scripts/brew-acceptance.py
```

生成 Apple Silicon / Intel 二进制包、包含固定依赖的源码包、SHA256、实际链接模块清单、第三方许可和 Formula。源码 Formula 在构建阶段离线使用固定依赖；不创建后台服务。发布工作流仅生成候选产物，不自动推送仓库或发布 Release。独立 Tap 与下载 URL 在公开发布后才可使用。

`brew-acceptance.py` 使用唯一的临时 Tap、独立 keg-only Formula 和测试 Keychain 项，验证安装、升级、删除旧 Cellar、凭据助手与卸载保留存档；不链接进用户 PATH、不激活真实账户。预发布系统没有新 Go bottle 时，可加 `--use-installed-go` 使用已有 Go，结果会标明依赖安装未作标准验收。

CLI/TUI 保持一个 Go 程序，无常驻服务、WebView 或代理。GUI 预留为独立构建，优先评估 Wails 复用 Engine；当前 CLI 不引入 GUI 依赖。

发布前执行 Go 官方 `govulncheck`，同时扫描源码和两个架构的二进制。开发扫描器不作为应用运行依赖；项目最低 Go 补丁版本为 1.26.9，以避开旧工具链已报告的标准库漏洞。

所有二级选择菜单的“返回”统一位于最后，Enter 确认或 Esc 返回。切换默认当前账户；删除、会话续接和账户详情默认返回。主菜单以绿色 CS 字符标识、蓝色名称和链接展示，仅在至少 60 列、24 行时展开，较小窗口保持单行标题；“退出”位于主菜单末尾。
