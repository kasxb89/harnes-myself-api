/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
export const RELAY_ACCESS_GUIDE_TITLE = '云枢 AI 中转接入说明书'
export const RELAY_ACCESS_GUIDE_FILENAME = '云枢AI_中转接入说明书.md'
export const RELAY_ACCESS_GUIDE_INTRO =
  '云枢 AI 是您的服务提供方。使用云枢 AI 的接口地址、您自己的中转 API Key 和模型 ID，即可接入支持相应协议的应用。'

export interface RelayAccessGuideSection {
  id: string
  titleKey: string
  markdown: string
}

/** Normalize a gateway root, preserving its deployment prefix, not a full endpoint. */
export function normalizeRelayRoot(value: unknown): string | undefined {
  if (typeof value !== 'string' || !value.trim()) return undefined
  const raw = value.trim()
  if (/[?#]|\p{Cc}/u.test(raw)) return undefined
  try {
    const url = new URL(raw)
    const loopback =
      url.hostname === 'localhost' ||
      url.hostname === '[::1]' ||
      /^127(?:\.\d{1,3}){3}$/u.test(url.hostname)
    if (
      (url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback)) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    ) {
      return undefined
    }
    const decodedPath = decodeURIComponent(url.pathname)
    if (/\\|\p{Cc}/u.test(decodedPath)) return undefined
    let path = url.pathname.replace(/\/+$/u, '')
    if (path.endsWith('/v1')) path = path.slice(0, -3)
    if (
      path.endsWith('/v1') ||
      /\/(?:chat\/completions|responses|messages|models)$/u.test(path)
    ) {
      return undefined
    }
    url.pathname = path.replaceAll('|', '%7C')
    return url.toString().replace(/\/+$/u, '')
  } catch {
    return undefined
  }
}

export function getRelayAccessGuideSections(
  rootURL?: string
): readonly RelayAccessGuideSection[] {
  const root = normalizeRelayRoot(rootURL)
  const address = root ?? '$YUNSHU_ROOT'
  const rootDescription = root
    ? `本页提供的接口根地址为：\`${root}\`。下文用 **R** 表示这个地址。`
    : '先从控制台复制接口根地址，下文用 **R** 表示。`$YUNSHU_ROOT` 是待填写的地址变量，不是可以直接访问的网址。'
  return [
    {
      id: 'account',
      titleKey: 'Account and balance',
      markdown: `## 1. 账户与余额

按注册页面提示完成注册并登录。可用的注册、邮箱验证及第三方登录方式，以当前页面为准。

在控制台查看账户余额、充值方式、模型价格和调用记录。**账户余额与 API Key 额度分别生效**：API Key 设置为不限额度，仍然受账户余额及计费设置约束。`,
    },
    {
      id: 'api-key',
      titleKey: 'API relay key',
      markdown: `## 2. 创建中转 API Key

进入 **API 令牌／API Keys**，创建一个用于当前应用的令牌，再复制完整 Key。检查有效期、可用额度、模型范围和 IP 限制。

应用中填写这里创建的**中转 API Key**。桌面客户端登录凭据、浏览器登录 JWT 和安全设置中的个人访问令牌（PAT），不能代替它。

请保管好 Key，不要放进公开代码、截图或求助消息。排查时提供发生时间、模型 ID、错误详情和请求编号即可。`,
    },
    {
      id: 'endpoints',
      titleKey: 'Endpoint and protocol',
      markdown: `## 3. 地址与接口协议

${rootDescription}

保留地址中的部署前缀，例如 \`/harness\`。**接口根地址、Base URL 和完整请求地址是不同字段**，不要重复添加 \`/v1\`。

| 应用字段或用途 | 填写内容 |
| --- | --- |
| 服务提供方／供应商名称 | 云枢 AI |
| API Key | 您自己的云枢 AI 中转 API Key |
| 接口根地址 R | \`${address}\` |
| OpenAI 兼容 Base URL | \`${address}/v1\` |
| Chat Completions 完整地址 | \`${address}/v1/chat/completions\` |
| Responses 完整地址 | \`${address}/v1/responses\` |
| Messages 完整地址 | \`${address}/v1/messages\` |
| 模型目录完整地址 | \`${address}/v1/models\` |

填写 Base URL 的应用通常自行追加接口路径；填写完整请求地址的应用需要包含最后的 /chat/completions、/responses 或 /messages。按应用当前字段说明选择，并确认所选模型支持该协议。`,
    },
    {
      id: 'models',
      titleKey: 'Choose a model',
      markdown: `## 4. 选择当前可用模型

从控制台模型目录或当前 Key 的 \`GET R/v1/models\` 返回结果中，复制**完整模型 ID**。实际请求使用 \`data[].id\`，保留前缀、大小写、连字符和小数点；显示名称可以自定义。

目录会随模型上下架、账户分组和令牌权限变化，以当前目录为准。模型在列表中出现，不代表它支持所有协议、图片理解、工具调用或全部参数。结合目录提供的 \`supported_endpoint_types\` 和控制台能力说明选择。

可以先读取目录确认地址和 Key，**这不会提交生成请求**：

\`\`\`sh
# 先设置 YUNSHU_ROOT 为接口根地址（末尾不带 /v1）。
# 设置 YUNSHU_API_KEY 为您自己的中转 API Key。
curl --request GET "$YUNSHU_ROOT/v1/models" \\
  --header "Authorization: Bearer $YUNSHU_API_KEY"
\`\`\`

需要验证生成时，选择支持目标协议的模型，发送一次简短请求即可。生成测试按正常价格计费。`,
    },
    {
      id: 'clients-and-errors',
      titleKey: 'Clients and troubleshooting',
      markdown: `## 5. 客户端配置与常见问题

| 客户端或方式 | 如何选择 |
| --- | --- |
| OpenAI 兼容应用 | 按应用实际使用的 Chat Completions 或 Responses 协议，选择支持该协议的模型；Base URL 填 R/v1。 |
| Claude Code | 原生接入使用 Messages 协议，模型还需支持应用所需的工具能力。只有模型名称相近，不代表全部功能兼容。 |
| Codex | 按当前版本的协议要求配置。Chat Completions 和 Responses 不能直接互换；选择已支持相应协议和所需能力的模型。 |
| CC-Switch | 可优先使用控制台现有的“导入 CC Switch”。手动配置时，先选择应用和协议，再按当前版本字段说明填写；本地路由转换与原生直连的地址格式可能不同。 |

### 用 CC-Switch 导入

1. 在控制台进入 API 令牌，找到要使用的 Key，选择“导入 CC Switch”。
2. 选择 Claude（用于 Claude Code）、Codex 或 Gemini，填写名称，并选择当前目录里的模型 ID。
3. 点击“打开 CC Switch”，在 CC-Switch 中确认导入并启用。
4. 返回原应用，按提示加载配置。

不同模型支持的协议和工具能力不同，请按所选应用需要的能力选择。

| 错误或现象 | 优先检查 |
| --- | --- |
| 401／认证失败 | Key 是否完整、停用或过期，是否误用了登录凭据，以及令牌本身额度是否耗尽。 |
| 403，详情含 insufficient_user_quota 或剩余额度不足 | 账户余额或本次请求预占额度不足，查看钱包和令牌额度。部分应用会将其显示为“Credentials rejected”，请以详情为准。 |
| 其他 403 | 模型权限、分组、IP 限制或账户状态，不要一律视为余额问题。 |
| 404／Invalid URL | 是否漏掉部署前缀、重复添加 /v1，或混淆完整请求地址与 Base URL。 |
| 429 | 请求频率或并发限制，降低频率和并发，并先核对原请求状态。 |
| 模型不存在／没有可用渠道 | 刷新当前 Key 的模型目录，核对完整 ID、权限和目标协议。模型不可用也可能返回其他状态码。 |
| 超时、断流或已扣费却没拿到结果 | 保留请求／任务编号，先查询调用日志或原任务结果，不要连续重发。 |

**已受理、已返回任务编号或状态不明时，继续查询原任务，不要重新创建。** 通用 Chat／Responses 请求不能假定每次重发都有跨请求幂等保障；断流也不代表没有扣费。

如需协助，请向云枢 AI 提供发生时间、完整模型 ID、请求／任务编号和报错原文，并遮住 API Key。`,
    },
  ]
}

export function buildRelayAccessGuide(rootURL?: string): string {
  const sections = getRelayAccessGuideSections(rootURL)
  return `# ${RELAY_ACCESS_GUIDE_TITLE}\n\n${RELAY_ACCESS_GUIDE_INTRO}\n\n${sections.map((section) => section.markdown).join('\n\n')}\n`
}
