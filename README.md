# CPA Model Health Monitor

CLIProxyAPI 原生 Go 插件。它分别检测 AI 提供商连通性、凭证可读取性和“渠道 × 凭证 × 模型”真实调用，并且只在稳定状态发生变化时发送 SMTP 邮件。

## 功能

- 管理页面可以从 CPA 自动发现并同步已启用的提供商、凭证和模型，也可以手工配置检测项。
- 三种检测层级可以独立建立监测项：提供商连通性、凭证可读取性、模型真实调用。
- 自动发现的模型通过 CPA 自身路由和已有凭证执行；新发现的模型默认关闭，用户勾选后即可启用真实检测。
- 凭证支持直接密钥，或绑定 CPA 的稳定 `auth_index` 后从认证文件实时读取。
- 支持 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Gemini GenerateContent 和 Codex Responses/SSE。
- 同时检查 HTTP 2xx、响应格式、完整结果和随机算术校验值。
- 首次探测默认只建立基线；后续仅在 `UP → DOWN` 或 `DOWN → UP` 时通知。
- 支持连续失败/恢复阈值。邮件发送失败会保留待通知状态，在后续检测中重试；成功后不会重复发送。
- 配置、状态和最近 200 次检测记录以 `0600` 权限原子写入插件数据目录。

## 安装

CPA 需要启用 Management API 和原生插件：

```yaml
remote-management:
  secret-key: "请使用强管理密钥"

plugins:
  enabled: true
  dir: "plugins"
  configs:
    cpa-model-health-monitor:
      enabled: true
      priority: 1
      interval_min: 30
      timeout_sec: 30
      failure_threshold: 1
      recovery_threshold: 1
      notify_initial: false
      max_concurrency: 4
```

将对应架构的动态库安装并重命名为：

```text
plugins/linux/amd64/cpa-model-health-monitor.so
plugins/linux/arm64/cpa-model-health-monitor.so
```

重启 CPA 后，从插件菜单打开 **Model Health Monitor**，或访问：

```text
http://<CPA_HOST>:8317/v0/resource/plugins/cpa-model-health-monitor/panel
```

面板请求使用 CPA 的 `remote-management.secret-key` 鉴权。已有密钥和 SMTP 密码不会返回给浏览器；密码框留空表示保留原值。

## 数据目录

默认数据目录：

- 容器内存在 `/CLIProxyAPI/plugins` 时：`/CLIProxyAPI/plugins/cpa-model-health-monitor`
- 其他环境：当前工作目录下的 `plugins/cpa-model-health-monitor`

可以设置 `CPA_MODEL_MONITOR_DATA_DIR` 覆盖。Docker 部署必须将该目录放在持久卷中，例如：

```yaml
services:
  cli-proxy-api:
    volumes:
      - ./plugins:/CLIProxyAPI/plugins
```

目录中的文件：

- `config.json`：面板配置，包含直接密钥和 SMTP 密码，权限为 `0600`。
- `state.json`：每个监测项的稳定状态、连续计数和通知状态，不含凭证。
- `history.json`：最近 200 次检测，不含请求头、密钥和原始响应。

## 配置监测项

每个监测项需要唯一 ID 和显示名称，然后选择检测层级：

面板中的“从 CPA 导入 / 同步”会读取现有认证文件和每个认证文件关联的模型。同步项使用稳定 ID 去重，再次同步会更新自动发现项并保留手工项及已有启用状态。提供商和凭证监测默认启用；模型调用可能产生费用，因此首次发现的模型默认关闭。

- `提供商连通性`：对 Base URL 发起不带凭证的 GET。HTTP 1xx–4xx 表示提供商端点可达，网络错误、超时或 HTTP 5xx 表示不可用。401/403 只说明端点要求鉴权，不会误判为提供商宕机。
- `凭证可读取性`：确认直接密钥或指定 CPA `auth_index` 能解析出令牌，不向上游模型发请求。
- `模型真实调用`：使用协议、Base URL、凭证和模型发起最小真实请求，并校验随机算术结果。

凭证来源：

- `直接密钥`：密钥加密传输到 CPA 管理接口，并写入权限受限的插件配置文件。
- `CPA auth_index`：插件只保存 `auth_index`，每次检测通过 `host.auth.get` 读取该凭证。默认依次查找 `access_token`、`api_key`、`token`、`key`；特殊结构可以填写点号分隔的“令牌 JSON 路径”。
- `CPA 自动路由`：不保存 Base URL、密钥或凭证索引，通过 `host.model.execute` 复用 CPA 已配置的渠道与凭证，适用于自动发现的模型监测。

鉴权方式：

- OpenAI/Codex 通常使用 `Bearer`。
- Anthropic 通常使用 `x-api-key`。
- Gemini API Key 通常使用 `Query key`。
- 需要额外版本或渠道头时，在“额外请求头”中填写 JSON 对象。

Base URL 可以填写服务根地址、`/v1` 地址或完整接口 URL。插件会避免重复拼接 `/v1`。

## 状态与通知语义

首次成功或失败只建立基线，除非启用“首次状态也通知”。稳定状态为可用时，连续达到失败阈值才切换为不可用；稳定状态为不可用时，连续达到恢复阈值才切换为可用。探测状态不变时不发送重复邮件。

SMTP 支持 STARTTLS、隐式 TLS 和无 TLS。生产环境建议使用 STARTTLS 或隐式 TLS。

## 管理 API

所有接口都受 CPA Management API 鉴权保护：

```text
GET  /v0/management/plugins/cpa-model-health-monitor/status
GET  /v0/management/plugins/cpa-model-health-monitor/settings
PUT  /v0/management/plugins/cpa-model-health-monitor/settings
GET  /v0/management/plugins/cpa-model-health-monitor/auth-files
GET  /v0/management/plugins/cpa-model-health-monitor/history
POST /v0/management/plugins/cpa-model-health-monitor/run
POST /v0/management/plugins/cpa-model-health-monitor/test-email
```

`POST /run` 传入 `{"wait":true}` 可以等待本轮完成。

## 构建与验证

本地已有 Go 1.24 和 C 编译器时：

```bash
go test ./...
go vet ./...
```

Linux 动态库使用 Docker Buildx 构建：

```bash
ARCH=amd64 ./build.sh
ARCH=arm64 ./build.sh
```

产物位于 `dist/`。

推送 `v*` 标签时，GitHub Actions 会自动构建 linux/amd64 和 linux/arm64，按照 CPA 商店要求生成
`cpa-model-health-monitor_<版本>_linux_<架构>.zip`、`checksums.txt`，并发布 GitHub Release。

仓库根目录的 `registry.json` 是 CPA schema v1 注册表。尚未收录到官方商店时，可以先把它作为自定义商店源：

```yaml
plugins:
  store-sources:
    - "https://raw.githubusercontent.com/imHansiy/cpa-model-health-monitor/main/registry.json"
```

也可以将 `registry.json` 中的插件条目提交到官方 [CLIProxyAPI Plugins Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store)。

## 参考实现

本插件保留了经过验证的实现边界，并针对通用模型监控重新实现：

| 项目 | 借鉴部分 | 本项目的调整 |
| --- | --- | --- |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | ABI v1、Management API、`host.auth.*` 和 `host.http.do` | 只使用官方宿主能力，不接管 CPA 的正常路由 |
| [codex-health-monitor](https://github.com/hg3386628/codex-health-monitor) | 独立 `auth_index` 探测、Codex SSE 完成校验、插件面板生命周期 | 扩展为多协议、多模型、SMTP 和状态转换 |
| [Relay Pulse](https://github.com/prehisle/relay-pulse) | 真实输出验证、连续失败/恢复阈值、首次基线不通知 | 状态按单一渠道凭证模型组合持久化 |
| [account-health-pushover](https://github.com/NoorChasib/cpa-plugin-account-health-pushover) | 通知发送成功后再标记、恢复通知、持久去重 | 通知器替换为 SMTP，取消周期提醒 |

这些项目均使用 MIT 许可证。详见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## 当前边界

- CPA 的 `host.model.execute` 当前不支持指定 `auth_index`。自动发现的模型监测用于验证该模型在 CPA 路由池中的整体可用性；若需要固定检查某一个账号，仍可建立手工模型监测项并绑定具体 `auth_index`。
- OAuth 提供商可能改变私有端点或认证字段。Codex 有专用适配；其他 OAuth 渠道可通过 Base URL、协议、鉴权方式和 JSON 路径配置，但仍需按该提供商实际接口验证。
- 插件与 CPA 同进程运行。若整个 CPA 进程或主机宕机，插件无法发送故障邮件；这类故障仍需要一个外部存活监控。

## License

MIT
