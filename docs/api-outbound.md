# 出站（Outbound）动态更新 HTTP API 协议文档

> 适用版本：lingjimarket-proxy（基于 sing-box 1.13.x 定制分支）
>
> 本文档描述如何通过 HTTP API **动态创建 / 替换出站节点**，并**切换 Selector 出站组**，
> 供外部程序在运行时控制代理行为，无需重启、无需重载配置文件。
>
> 相关实现代码：
> - 自定义扩展接口 `POST /outbounds`：`experimental/clashapi/outbounds.go`
> - 标准 Clash API（`/proxies` 系列）：`experimental/clashapi/proxies.go`
> - 出站管理器（创建/移除/依赖检查）：`adapter/outbound/manager.go`
> - Selector 切换逻辑：`protocol/group/selector.go`

---

## 1. 通用约定

### 1.1 Base URL

```
http://<host>:<port>
```

即配置文件中 `experimental.clash_api.external_controller` 的监听地址，例如 `127.0.0.1:9090`。

启用前置条件（config.json）：

```json
{
  "experimental": {
    "clash_api": {
      "external_controller": "127.0.0.1:9090",
      "secret": "your-secret"
    }
  }
}
```

### 1.2 认证

| 项目 | 说明 |
|---|---|
| 方式 | HTTP Header `Authorization: Bearer <secret>` |
| secret 为空 | 所有接口免认证 |
| secret 非空 | 所有接口必须携带 Header，格式严格为 `Bearer` + **单个空格** + secret（大小写敏感） |
| 认证失败 | 返回 `401` + `{"message": "Unauthorized"}` |

```http
Authorization: Bearer your-secret
```

### 1.3 请求/响应格式

- 请求体：`Content-Type: application/json`（UTF-8）
- 错误响应统一格式：`{"message": "<错误描述>"}`（对应源码 `HTTPError`）
- CORS：默认 `Access-Control-Allow-Origin: *`（可通过 `access_control_allow_origin` 收紧），支持浏览器直连

### 1.4 接口总览

| 方法 | 路径 | 用途 | 来源 |
|---|---|---|---|
| `POST` | `/outbounds` | **创建或替换一个出站**（节点热更新） | 本 fork 自定义扩展 |
| `GET` | `/proxies` | 列出全部出站（含组信息，用于校验/发现） | Clash API |
| `GET` | `/proxies/{name}` | 查询单个出站详情（`now`/`all`） | Clash API |
| `PUT` | `/proxies/{name}` | **切换 Selector 出站组的当前选择** | Clash API |
| `GET` | `/proxies/{name}/delay` | 对单个出站做延迟测试 | Clash API |
| `GET` | `/group` / `/group/{name}` | 列出/查询出站组 | Clash Meta API |
| `GET` | `/group/{name}/delay` | 对组内全部出站批量测速 | Clash Meta API |

---

## 2. 核心接口一：创建/替换出站 `POST /outbounds`

### 2.1 语义

- 请求体为**一个完整的出站配置对象**（与 config.json 中 `outbounds` 数组的单个元素结构完全一致，扁平结构）。
- 按 `tag` 判定行为：
  - tag 不存在 → **创建**（CREATED）
  - tag 已存在 → **替换**（先移除旧出站、关闭其连接，再创建新出站并立即启动）
- 替换/创建成功后，**新连接立即使用新配置**（路由器在每个连接建立时动态查找出站，见 `route/route.go`），已建立的旧连接按原配置继续直至关闭。

### 2.2 请求格式

```
POST /outbounds
Authorization: Bearer <secret>
Content-Type: application/json

{
  "tag": "<出站唯一标识，必填>",
  "type": "<出站类型，必填>",
  ...该类型特有的配置字段（与 tag/type 同级、扁平展开）...
}
```

### 2.3 成功响应

```
HTTP/1.1 200 OK

{
  "action": "CREATED" | "REPLACE",
  "outbound": {
    "tag": "<tag>",
    "type": "<type>"
  }
}
```

> 注意：`action` 的替换取值为字面量 `REPLACE`（源码 `experimental/clashapi/outbounds.go:49`）。

### 2.4 参数校验规则（客户端必须在提交前自检）

服务端按以下顺序校验，任一失败即返回错误：

| # | 校验项 | 失败响应 | 说明 |
|---|---|---|---|
| 1 | JSON 语法可解析 | `400` `Invalid request body(read/unmarshal): ...` | 请求体必须是合法 JSON 对象 |
| 2 | `type` 字段已知 | `400` `unknown outbound type: <type>` | 见 §5.1 支持的类型清单 |
| 3 | `tag`、`type` 非空 | `400` `Tag and type are required` | |
| 4 | 无未知字段 | `400` `unexpected key: <field>` 或 `json: unknown field ...` | 反序列化使用 `DisallowUnknownFields`，**多传任何该类型不支持的字段都会被拒绝** |
| 5 | 类型字段合法性 | `400`（具体校验错误信息） | 如端口越界、必填子字段缺失（如 shadowsocks 的 `method`/`password`） |
| 6 | （替换时）旧出站未被依赖 | `500` `Failed to remove existing outbound: outbound[<tag>] is depended by <tags>` | 详见 §2.6 |
| 7 | 新出站能成功构建并启动 | `500` `Failed to create new outbound: ...` | 如 Selector 成员不存在、TLS 配置非法、依赖的 detour 不存在等 |

### 2.5 示例

创建一个 Shadowsocks 节点：

```bash
curl -X POST http://127.0.0.1:9090/outbounds \
  -H "Authorization: Bearer your-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "tag": "node-hk-01",
    "type": "shadowsocks",
    "server": "1.2.3.4",
    "server_port": 8388,
    "method": "2022-blake3-aes-128-gcm",
    "password": "8JCsPssfgS8tiRwiMlhARg=="
  }'
```

替换（热更新）同名节点的服务器地址与端口（请求体必须**完整**，不是增量 patch）：

```bash
curl -X POST http://127.0.0.1:9090/outbounds \
  -H "Authorization: Bearer your-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "tag": "node-hk-01",
    "type": "shadowsocks",
    "server": "5.6.7.8",
    "server_port": 8389,
    "method": "2022-blake3-aes-128-gcm",
    "password": "8JCsPssfgS8tiRwiMlhARg=="
  }'
# → {"action":"REPLACE","outbound":{"tag":"node-hk-01","type":"shadowsocks"}}
```

### 2.6 ⚠️ 依赖限制（最重要的集成约束）

出站之间存在**依赖关系**（dependency），在创建出站时注册：

- **selector / urltest 组**依赖其 `outbounds` 列表中的每一个成员；
- 任何出站的 `detour` 字段（链式转发）依赖其指向的出站。

**当目标 tag 被其他出站依赖时，`POST /outbounds` 的替换会失败**，返回：

```
HTTP/1.1 500

{"message": "Failed to remove existing outbound: outbound[node-hk-01] is depended by proxy-group"}
```

因此：

| 操作 | 是否允许 |
|---|---|
| 创建全新 tag 的出站 | ✅ 总是允许 |
| 替换一个**未被任何组引用、无人 detour** 的出站 | ✅ 允许 |
| 替换一个**是 selector/urltest 成员**或**被 detour 引用**的出站 | ❌ 500 失败 |
| 替换 selector / urltest **组本身**（组被路由规则引用不算依赖） | ✅ 允许 |

> 重要：路由规则（`route.rules[].outbound`）对出站的引用**不构成**依赖，因此被规则直接指向的出站/组可以被替换。推荐的动态换节点方案见 §4 场景 C。

---

## 3. 核心接口二：切换 Selector `PUT /proxies/{name}`

### 3.1 语义

将名为 `{name}` 的 **selector 类型出站组**的当前出口切换为指定成员。`{name}` 需 URL 转义（支持中文/特殊字符 tag）。

### 3.2 请求格式

```
PUT /proxies/{selector的tag}
Authorization: Bearer <secret>
Content-Type: application/json

{
  "name": "<目标成员的tag，必须是该组 outbounds 列表中的成员>"
}
```

### 3.3 响应

| 状态码 | 含义 |
|---|---|
| `204 No Content` | 切换成功（**响应体为空**） |
| `400` `Must be a Selector` | `{name}` 不是 selector 类型（urltest/普通节点均不可） |
| `400` `Selector update error: not found` | 目标成员不在该组的 `outbounds` 列表中 |
| `400` `Body invalid` | 请求体非法/缺失 `name` 字段 |
| `404` `Resource not found` | `{name}` 对应的出站不存在 |

### 3.4 切换的内部逻辑（决定客户端应如何使用）

1. 切换**立即生效**：selector 在每个新连接上动态读取当前选择；
2. **持久化**：若启用了 `experimental.cache_file`（`store_selected` 默认开启），选择会写入缓存文件，进程重启后自动恢复；
3. **存量连接**：默认**不打断**已建立连接；仅当该 selector 配置了 `interrupt_exist_connections: true` 时，切换会中断组内旧连接；
4. 重复选择当前已选中的成员：成功（204），无副作用。

### 3.5 示例

```bash
# 切换组 "proxy" 到成员 "node-hk-01"
curl -X PUT http://127.0.0.1:9090/proxies/proxy \
  -H "Authorization: Bearer your-secret" \
  -H "Content-Type: application/json" \
  -d '{"name": "node-hk-01"}'
# → HTTP 204
```

---

## 4. 状态查询与验证接口

### 4.1 `GET /proxies` — 列出全部出站

返回 `{"proxies": { "<tag>": <ProxyInfo>, ... }}`，每个 `ProxyInfo`：

```json
{
  "type": "Selector",
  "name": "proxy",
  "udp": true,
  "now": "node-hk-01",
  "all": ["node-hk-01", "node-us-01"],
  "history": [{"time": "...", "delay": 42}]
}
```

- `type`：出站类型显示名（block 显示为 `Reject`）
- `now` / `all`：**仅组类型（selector/urltest）返回**——`now` 是当前选中成员，`all` 是成员列表
- 响应中额外包含一个合成的 `GLOBAL` 条目（兼容 Clash 面板，勿当作真实出站操作）

用途：**提交前校验 tag 是否存在、切换后验证 `now` 是否符合预期**。

### 4.2 `GET /proxies/{name}` — 查询单个出站

`{name}` URL 转义。404 表示 tag 不存在。selector 返回的 `now` 即当前出口，是最可靠的切换结果验证手段。

### 4.3 `GET /proxies/{name}/delay` — 延迟测试

Query 参数：

| 参数 | 必填 | 说明 |
|---|---|---|
| `timeout` | 是 | 超时毫秒数（整数） |
| `url` | 否 | 测试 URL，默认 `https://www.gstatic.com/generate_204` |

响应：`200 {"delay": 123}`（毫秒）；`503` 测试失败；`504` 超时；`400` 参数非法。

### 4.4 `GET /group/{name}/delay` — 组批量测速

返回 `{"<成员tag>": <delay>, ...}`，仅对可用成员返回键值。参数同上。适合切换前做健康筛选。

---

## 5. 参数参考

### 5.1 支持的出站 `type` 清单

| type | 说明 | 备注 |
|---|---|---|
| `direct` | 直连 | |
| `block` | 拒绝 | |
| `selector` | 手动选择组 | 可被 `PUT /proxies/{name}` 切换 |
| `urltest` | 自动测速组 | 不可用 PUT 切换 |
| `shadowsocks` | SS | |
| `vmess` / `vless` / `trojan` | | |
| `http` / `socks` | | |
| `naive` / `ssh` / `tor` / `shadowtls` / `anytls` | | |
| `hysteria` / `hysteria2` / `tuic` | QUIC 系 | 需构建标签 `with_quic`，否则创建报错 |
| `shadowsocksr` / `wireguard` | 已移除 | 创建时返回明确错误，勿使用 |

### 5.2 通用字段（绝大多数出站类型共享）

```json
{
  "tag": "string, 必填",
  "type": "string, 必填",
  "server": "string, 服务器地址",
  "server_port": 0,

  "detour": "string, 可选, 上游出站tag（构成依赖）",
  "domain_resolver": "string 或对象, 可选, 域名解析服务器",
  "connect_timeout": "duration字符串, 可选",
  "tcp_fast_open": false,
  "tcp_multi_path": false
}
```

> 完整字段与配置语法同官方 sing-box 文档的 Outbound 配置；所有字段均可用于 `POST /outbounds`。

### 5.3 常用类型完整请求体示例

**selector（组）：**

```json
{
  "tag": "proxy",
  "type": "selector",
  "outbounds": ["node-hk-01", "node-us-01", "direct"],
  "default": "node-hk-01",
  "interrupt_exist_connections": false
}
```

**urltest（自动组）：**

```json
{
  "tag": "auto",
  "type": "urltest",
  "outbounds": ["node-hk-01", "node-us-01"],
  "url": "https://www.gstatic.com/generate_204",
  "interval": "3m",
  "tolerance": 50
}
```

**vmess：**

```json
{
  "tag": "node-hk-01",
  "type": "vmess",
  "server": "vm.example.com",
  "server_port": 443,
  "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
  "security": "auto",
  "alter_id": 0,
  "tls": {
    "enabled": true,
    "server_name": "vm.example.com",
    "insecure": false
  },
  "transport": {
    "type": "ws",
    "path": "/path",
    "headers": { "Host": "vm.example.com" }
  }
}
```

**trojan：**

```json
{
  "tag": "node-us-01",
  "type": "trojan",
  "server": "tj.example.com",
  "server_port": 443,
  "password": "pass",
  "tls": { "enabled": true, "server_name": "tj.example.com" }
}
```

**vless：**

```json
{
  "tag": "node-jp-01",
  "type": "vless",
  "server": "vl.example.com",
  "server_port": 443,
  "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
  "flow": "xtls-rprx-vision",
  "tls": { "enabled": true, "utls": { "enabled": true, "fingerprint": "chrome" } }
}
```

**http / socks：**

```json
{ "tag": "http-proxy", "type": "http", "server": "1.2.3.4", "server_port": 8080, "username": "u", "password": "p" }
```

```json
{ "tag": "socks-proxy", "type": "socks", "server": "1.2.3.4", "server_port": 1080, "version": "5" }
```

---

## 6. 典型集成场景（推荐调用序列）

### 场景 A：在既有组成员之间切换（最简单）

```bash
# 1. (可选) 查看组成员与当前选择
curl -H "Authorization: Bearer $SECRET" http://127.0.0.1:9090/proxies/proxy
# 2. 切换
curl -X PUT http://127.0.0.1:9090/proxies/proxy \
  -H "Authorization: Bearer $SECRET" -H "Content-Type: application/json" \
  -d '{"name": "node-us-01"}'
# 3. 验证：确认 now == node-us-01
curl -H "Authorization: Bearer $SECRET" http://127.0.0.1:9090/proxies/proxy
```

### 场景 B：热更新未被引用的节点配置

适用于该节点 tag 未被任何 selector/urltest 引用、也无人 detour 它（例如路由规则直接指向它）。

```bash
curl -X POST http://127.0.0.1:9090/outbounds \
  -H "Authorization: Bearer $SECRET" -H "Content-Type: application/json" \
  -d '{ "tag": "node-standalone", "type": "shadowsocks", ...完整配置... }'
# → {"action":"REPLACE", ...}
```

### 场景 C：新增节点并纳入选择器（完整的动态换节点流程）⭐ 推荐

由于**被组引用的节点不能直接替换**（§2.6），标准做法是「先建新节点，再整体替换组，最后显式切换」：

```bash
# 1. 创建全新 tag 的节点（新 tag 一定不会被依赖，必然 CREATED）
curl -X POST http://127.0.0.1:9090/outbounds ... -d '{
  "tag": "node-hk-02", "type": "shadowsocks", "server": "5.6.7.8", ... }'

# 2. 用新的成员列表整体替换 selector 组（组的 outbounds 必须包含第1步的新 tag，
#    否则组启动校验失败返回 500；不想要的旧节点可从列表中去掉）
curl -X POST http://127.0.0.1:9090/outbounds ... -d '{
  "tag": "proxy", "type": "selector",
  "outbounds": ["node-hk-02", "node-us-01"], "default": "node-hk-02" }'

# 3. 显式切换到目标节点（必须执行！原因见下方说明）
curl -X PUT http://127.0.0.1:9090/proxies/proxy ... -d '{"name": "node-hk-02"}'

# 4. 验证
curl http://127.0.0.1:9090/proxies/proxy ...   # now == "node-hk-02"
```

> **为何第 3 步必须执行**：重建同名 selector 时，若启用了 cache_file 且旧的选择成员仍在
> 新成员列表中，服务端会**优先恢复缓存中的旧选择**（优先级：缓存选择 > `default` > 第一个成员）。
> 因此外部程序在重建组之后应**总是显式调用 PUT 完成切换**，不要依赖 `default` 字段。

### 切换前健康检查（可选）

```bash
curl "http://127.0.0.1:9090/group/proxy/delay?url=https://www.gstatic.com/generate_204&timeout=5000" \
  -H "Authorization: Bearer $SECRET"
# → {"node-hk-02": 42, "node-us-01": 187}
```

---

## 7. 错误码汇总与客户端处理建议

| HTTP 状态码 | 触发条件 | 客户端建议 |
|---|---|---|
| `200` | POST /outbounds 成功 | 依据 `action` 记录审计日志 |
| `204` | PUT 切换成功 | 用 GET 复核 `now` |
| `400` | 参数/JSON/字段校验失败 | **不要重试**，修正请求体；解析 `message` 定位字段 |
| `401` | 认证失败 | 检查 secret 与 `Bearer ` 前缀格式 |
| `404` | PUT/GET 的出站 tag 不存在 | 先 `GET /proxies` 重新发现可用 tag |
| `500` | 依赖冲突（`is depended by`）或创建/启动失败 | 依赖冲突改用场景 C 流程；启动失败检查成员/detour 是否存在 |
| `503` / `504` | 延迟测试失败/超时 | 换节点重测，不代表 API 异常 |

**最佳实践：**

1. **幂等性**：`POST /outbounds` 对同一请求体天然幂等（REPLACE 结果与 CREATED 一致）；PUT 幂等。可安全重试网络层错误（超时、5xx 中的启动失败除外）。
2. **顺序约束**：先创建节点，再创建/替换引用它的组（成员不存在时组创建返回 500）。
3. **完整提交**：POST /outbounds 是全量替换语义，客户端不得发送增量字段（未知字段直接 400）。
4. **避免并发写同一 tag**：服务端对出站管理器有锁保护，但「替换组 + 切换」应作为一个原子序列由单一调度方串行执行。
5. **tag 命名**：建议使用稳定的英文/数字 tag；含中文或特殊字符时所有路径参数需 URL 编码。
6. **变更后验证**：始终以 `GET /proxies/{selector}` 的 `now` 字段作为切换成功的判定依据（PUT 的 204 只代表指令受理）。
