# 服务商配置

## TOML 导入

服务商必须支持 OpenAI Responses API。仅支持 Chat Completions 的接口不能直接使用，工具不提供协议转换代理。

在「添加账户 → 第三方配置」粘贴 TOML，填写本地账户备注，再按 Ctrl+S。粘贴后可编辑，Tab 切换字段；默认明文显示密钥。Esc 放弃，保存过程中会先取消并等待清理。

```toml
model_provider = "relay"
model = "your-model-id"
review_model = "your-model-id"

[model_providers.relay]
name = "Relay"
base_url = "https://gateway.example/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "REPLACE_WITH_API_KEY"
```

`model_provider` 必须与对应配置表的名称一致，名称可以是 `relay`、`OpenAI` 等；添加命令不绑定某一家服务商。模型 ID 和接口地址需使用实际服务商提供的值。

```sh
codex-switch add provider relay --toml-file provider.toml
codex-switch use relay
codex-switch edit relay
```

导入只保存，不立即切换。导入不执行认证命令或读取环境变量，来源必须包含 `experimental_bearer_token`；MCP、hooks、插件、命令和本地路径不随账户导入。未知字段会提示未导入。

支持的账户字段包括 `review_model`、`disable_response_storage`、`network_access`、`windows_wsl_setup_acknowledged` 及 `features.api_key_model_discovery`、`features.goals`、`features.image_generation`。旧客户端字段可能被 Codex 忽略。provider 可保存重试次数、流超时、WebSocket 和独立搜索能力字段。

文件导入最多 1 MiB，仅接受普通文件。地址支持 HTTP/HTTPS 和查询参数，不接受内嵌用户名密码或片段。当前配置使用内联或点号形式的 `features` 时，需改为 `[features]` 表。

## 认证与图片授权

TOML 导入保留直接 bearer 模式。启用时密钥写入权限为 600 的 `config.toml` 和 `auth.json`。JSON 添加默认通过凭据助手提供密钥；该内部命令只供 Codex 调用，会输出密钥，不要收集其输出。

服务商要求图片执行器授权时，可以保留其请求头与功能配置，例如：

```toml
[model_providers.relay]
name = "Relay"
base_url = "https://gateway.example/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "REPLACE_WITH_API_KEY"
http_headers = { "x-openai-actor-authorization" = "local-image-extension" }

[features]
api_key_model_discovery = true
image_generation = true
```

该片段替换前例中的 provider 表；同一 TOML 中不要重复声明相同表。图片生成还需要客户端、扩展和上游图片模型路由共同支持。文本模型可用或配置导入成功不代表生图成功。

## 模型目录

模型管理针对每一个第三方账户，账户间互不影响。修改未启用账户只更新存档；修改当前账户通过切换事务生效，客户端完成后手动打开。

```sh
codex-switch models relay
codex-switch models relay 'model-a,model-b' model-a
```

手动目录最多 200 项，默认模型必须在列表中。可为模型配置显示名称与上下文窗口；列入目录不代表上游能够使用该模型。

远程目录通过 provider 的 `model_catalog_url` 设置，并启用 `features.api_key_model_discovery`。返回内容须符合 Codex 模型目录格式，普通 `/models` 响应不一定兼容。模型管理可在手动和已保存的远程目录之间选择。

JSON 文件添加示例：

```json
{
  "base_url": "https://gateway.example/v1",
  "api_key": "REPLACE_WITH_API_KEY",
  "model": "model-a",
  "models": [
    { "id": "model-a", "name": "主力模型", "context_window": 128000 },
    { "id": "model-b" }
  ]
}
```

用 `add provider relay --file provider.json` 导入。示例中的上下文窗口是演示值，应按模型能力修改。自定义请求头不能覆盖模型请求的 Authorization。

## 订阅与额度

在账户详情「额度配置」选择 Sub2API、通用模板或关闭查询。配置和缓存均加密，查询配置不写入 Codex 的模型配置。

```sh
codex-switch quota relay
codex-switch quota relay --file usage.json
codex-switch status relay --refresh
```

Sub2API 最简配置为 `{"type":"sub2api"}`，默认复用 API Key 并根据接口地址生成 `/v1/usage`。特殊部署可提供 `url`；独立查询凭据使用 `api_key`。显示钱包、订阅、Key 配额及重置时间，以服务端实际返回字段为准。

通用模板发送 GET/POST 请求，通过字段映射读取数据，不执行 JavaScript：

```json
{
  "type": "generic",
  "url": "https://gateway.example/api/balance",
  "headers": { "Authorization": "Bearer {{apiKey}}" },
  "fields": { "remaining": "data.balance", "plan": "data.plan" },
  "unit": "USD",
  "scale": 1
}
```

支持 `{{apiKey}}`、`{{baseUrl}}` 请求头和请求体替换；字段路径使用 `data.balance`、`data.items.0.balance` 或 JSON Pointer `/data/balance`。支持 `remaining`、`used`、`total`、`plan`、`unit`、`expires_at`、`is_valid`；数值换算系数省略时为 1。可以使用独立查询密钥。

未配置查询的第三方账户不会自动请求额度。查询失败展示原因、上次成功时间及缓存，不将失败当作余额为零。官方 credits、百分比和第三方货币余额分别展示，不用于精确账单结算。
