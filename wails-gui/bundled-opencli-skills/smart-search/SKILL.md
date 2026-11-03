---
name: smart-search
description: 基于 opencli 适配器的"垂直站点"搜索路由器。仅当用户明确指定第三方平台（小红书/微博/知乎/X/Twitter/Reddit/B 站/抖音/小红书/淘宝/京东/Boss 直聘/雪球/36kr 等），或意图明显属于"中文社区舆论 / 国内热点 / 社交媒体讨论 / 站内垂直检索"时才使用本 skill。**不要**用本 skill 处理一次性事实查询（天气、汇率、时间、IP、Wikipedia、公开 REST API、用户已给定 URL 的页面抓取等）——这些直接用 WebFetch / curl 更快更准。AI 源（grok/doubao/gemini/kimi/qwen 等）每题最多问 1 个，禁止把同一问题串发给多个 AI 站点
---

# 智能搜索路由器

根据话题和场景，将查询路由到最佳的 opencli 搜索源。此 skill 的核心目标不是记忆命令，而是先定位数据源，再让 Agent 通过 `opencli` 自己读取实时帮助，避免文档漂移。

## 不适用本 skill 的场景（必须直接走 WebFetch / Bash curl）

opencli 适配器是为"反爬严重、需要登录态、需要浏览器渲染"的垂直社区站点服务的。当问题有公开端点能直接拿到结构化结果时，绕开本 skill 是更快的路径。

以下场景**禁止**进入 opencli 预检流程，应直接 WebFetch：

- **天气**：`https://wttr.in/<城市>?format=j1`（JSON）或 `?format=3`（一行摘要）。中文城市直接 URL-encode 即可，例如 `https://wttr.in/天津?format=j1`。
- **时间 / 时区**：`https://worldtimeapi.org/api/timezone/<Area>/<City>`。
- **汇率**：`https://api.exchangerate.host/latest?base=USD&symbols=CNY` 之类。
- **IP / 地理位置**：`https://ipapi.co/<ip>/json/` 之类。
- **Wikipedia / 词条事实**：`https://zh.wikipedia.org/api/rest_v1/page/summary/<term>` 或 `en.wikipedia.org/...`。
- **GitHub / GitLab 公开仓库元数据**：`https://api.github.com/repos/<owner>/<repo>` 之类。
- **用户已经给出完整 URL**：无论是哪个域名，先 WebFetch 这个 URL；只有当抓取失败、且该域名恰好在 opencli registry 里时，才考虑切到 opencli。
- **任何已知有公开 REST/JSON 端点的事实型查询**。

判断口径：能用一两行 curl/WebFetch 拿到答案的，不要进 opencli。把 opencli 留给真正"没有公开 API、必须模拟浏览器/登录"的场景。

## 何时切回 WebFetch 兜底

即便已经进入了本 skill 的流程，以下任一条件成立就**立即停止**继续尝试 opencli 适配器，改用 WebFetch：

- opencli 预检（`opencli list -f yaml` + `opencli <site> -h`）累计已花费 ≥ 2 次 Bash 调用，仍未锁定一个明确可用的子命令。
- 已经真正执行过 1 个 opencli 子命令，结果为空 / 报错 / 验证码 / 登录失败。
- 用户问题的目标域名在 opencli registry 里**根本不存在**（例如天气、官方 API、政府公开数据）。
- 任一适配器返回 "auth-required" / "extension not connected" / "browser launch failed" 等环境问题。

切到 WebFetch 时，在最终回答里明确写出"opencli 不适用 → 已改用 WebFetch <URL>"，便于用户复盘。

## 扩展未连接时的用户提示（NEVER VIOLATE）

当 `opencli doctor`、`opencli list` 或任一适配器返回 `extension not connected` / `browser bridge not connected` / `Browser not connected` / `chromeExtInstalled: false`，**且**用户问的恰好是只能靠扩展拿到的垂直站点（知乎、微博、小红书、B 站、X/Twitter、Reddit、淘宝、Boss 直聘等需要登录态或反爬严重的站点），就算你已经按"处理不可用的源"切到 AI 源 / WebFetch 拿到了部分兜底结果，最终回答也**必须**加一段独立的安装引导，包含下面三件事：

1. **一句话说明**：这类站点需要 OpenCLI 的 Chrome 扩展才能稳定抓到（不要说"Browser Bridge"这种术语，用户是非技术用户）。
2. **可点击的 markdown 链接**（不要只贴裸 URL）：`[OpenCLI Chrome 扩展](https://chromewebstore.google.com/detail/opencli/ildkmabpimmkaediidaifkhjpohdnifk)`
3. **明确告诉用户装完回来重发同样的问题**就能拿到完整结果。

这段提示放在最终回答的**末尾**、"搜索摘要"之前，单独成段，用用户当前语言表达（中文 / 英文）。不要把它埋进长段落里，也不要和其它失败原因混排。

示例口径（中文，知乎热榜场景）：

> 顺带说一句：知乎热榜这类站点需要 OpenCLI 的 Chrome 扩展才能直接抓。你可以先去[安装 OpenCLI Chrome 扩展](https://chromewebstore.google.com/detail/opencli/ildkmabpimmkaediidaifkhjpohdnifk)，装完后回来再发一次"看看知乎热榜"，我就能给你完整列表。

示例口径（英文）：

> Heads up: pulling this kind of content (Zhihu hot list, Weibo, Xiaohongshu, etc.) needs the OpenCLI Chrome extension. Install it from the [Chrome Web Store](https://chromewebstore.google.com/detail/opencli/ildkmabpimmkaediidaifkhjpohdnifk), then re-send your question and I'll fetch the real list.

**什么时候不要加这段**：用户问题本来就有公开 API 兜底（天气、汇率、wiki、官方 REST 接口等），那些场景压根不该走 opencli，加扩展提示反而是噪音。

## 强制预检

每次使用前，必须先做下面两步：

- 运行 `opencli list -f yaml`
- 用 live registry 确认候选站点是否存在，并检查 `strategy`、`browser`、`domain`

**预检硬上限**：list 之后如果在 registry 里**找不到与用户问题领域明显匹配的站点**（典型例子：天气、官方 API、政府数据、单纯的事实问答），**立即放弃 opencli 路径**，直接走 WebFetch，不要再继续 `opencli <site> -h` 探测。

选定站点后，必须再做下面两步：

- 运行 `opencli <site> -h` 查看该站点有哪些子命令
- 若已锁定某个子命令，再运行 `opencli <site> <command> -h` 查看参数、输出列、策略

预检（`list` + 至多 1 个 `<site> -h` + 至多 1 个 `<site> <command> -h`）总和不应超过 3 个 Bash 调用。如果 3 个调用之后仍没锁定一个明确能用的子命令，按"不适用本 skill 的场景"切到 WebFetch。

不要在 skill 文档里硬编码参数或假设命令签名；以 `opencli ... -h` 的实时输出为准。

## 强制每次都加 `--site-session ephemeral`（NEVER VIOLATE）

每一次真正执行 opencli 适配器命令（不算 `list` / `-h` 预检），都**必须**加 `--site-session ephemeral` 这个 flag。例如：

- ❌ `opencli gemini chat "..."`
- ✅ `opencli gemini chat "..." --site-session ephemeral`
- ❌ `opencli reddit read --url https://...`
- ✅ `opencli reddit read --url https://... --site-session ephemeral`

**为什么**：很多适配器（gemini / google / 各种 AI 站点等）在 `cli-manifest.json` 里把自己标成了 `siteSession: persistent`。一旦 persistent 生效，扩展那边 `IDLE_TIMEOUT_NONE = -1`，opencli 启动的 Chrome 窗口**永远不会自动关闭**，会一直留在用户屏幕上直到 Yansu 退出。强制 `ephemeral` 后会按 30 秒 idle 自动关。

**何时不加**：用户明确说"接着上次那个 Gemini 对话继续问"这种**显式要求复用 session** 的场景。如果用户没明确说，永远默认 ephemeral，保护用户屏幕。

## 主路由规则

只使用这一条规则，不再维护多套优先级：

1. 当用户明确指定网站、平台或数据源时，直接使用对应网站。
2. 当用户没有指定网站时，优先只选择一个 AI 源：`grok`、`doubao`、`gemini` 三选一。
3. 当 AI 返回内容不足、缺少原始数据、需要权威佐证或需要垂直结果时，再补充 1-2 个专用源。

## 单题预算与频率限制

把“单个用户问题”理解为同一意图链路下的一次问题求解；同一轮追问、澄清、补充条件，若核心问题未变，仍算同一题。

先建立一份站点调用台账。每次真正执行搜索命令后，立刻更新：

- `site`
- `query`
- `count`
- `status`

计数规则：

- `opencli list -f yaml`、`opencli <site> -h`、`opencli <site> <command> -h` 属于预检与帮助，不计入搜索次数
- 一次真正的 `opencli <site> ...` 搜索/查询执行，计为该站点 1 次调用
- 同站点因为报错、超时、验证码、反爬、登录态异常而失败，也算 1 次调用，不要无限重试

频率上限：

- AI 站点硬限制：同一题内，每个 AI 站点最多调用 1 次
- 默认策略仍然是只选 1 个 AI 站点，不要把多个 AI 站点串成常规流程
- 只有当用户明确要求比较多个 AI 站点时，才可以额外调用其他 AI 站点；但每个被点名的 AI 站点仍然最多 1 次
- 非 AI 站点默认最多调用 2 次
- 非 AI 站点第 2 次调用必须有明确理由，例如第一次结果过宽，需要加时间、地区、类别、排序或关键词限定
- 非 AI 站点不要进行第 3 次调用；若信息仍不足，停止扩搜并明确说明缺口

触发限频后的处理：

- 记录：「已跳过：<site> 达到频率上限」
- 优先改用其他同类站点
- 若没有合适替代源，则直接基于已收集信息回答，并说明覆盖范围与缺口

## 查询结束汇报

每次查询结束后，回答末尾必须追加一段简短的“搜索摘要”，至少包含下面三项：

- 使用了什么网站搜索
- 每个网站搜了什么词
- 每个网站搜了几次

如果有被限频跳过的站点，也要明确写出。

建议使用下面的固定格式：

```md
搜索摘要
- 网站：<site1> | 查询词：<term1> | 次数：<n>
- 网站：<site2> | 查询词：<term2>；<term3> | 次数：<n>
- 已跳过：<site3>，原因：达到频率上限
```

## AI 源选择

- `grok`
  适合实时讨论、英文互联网舆论、Twitter/X 语境、热点追踪。
- `doubao`
  适合中文语境、字节抖音生态、生活方式内容、中文热点与泛中文问答。
- `gemini`
  适合全球网页、英文资料、通用信息检索、背景综述。

如果用户没有指定网站，默认先判断语言和语境，再从这三个里只选一个。

一旦某个 AI 站点已经执行过一次真实查询，就不要在同一题里改写关键词后再次调用该 AI 站点。若答案不足，优先补专用源，不要反复追打同一个 AI 站点。

## AI 查询词建议

当使用 AI 源时，不要只丢一个过短关键词。优先构造成“主题 + 目标 + 限定条件”的查询。

- 主题
  用户真正要查的对象、事件、产品、人物、公司、技术名词。
- 目标
  想要什么结果，例如总结、对比、原因、趋势、推荐、原始线索。
- 限定条件
  语言、地区、时间范围、平台范围、受众、价格带、岗位地点、是否要引用原始来源。

优先使用下面这种表达方式：

- `<主题> + <你要回答的问题>`
- `<主题> + <时间范围/地区/语言>`
- `<主题> + <平台或来源范围>`
- `<主题> + <输出要求>`

避免只输入：

- 单个名词
- 没有时间范围的热点问题
- 没有地区限制的购物、求职、旅游问题
- 没有平台限制的社交媒体问题

## 专用源补充时机

当出现以下任一情况时，再补充专用源：

- AI 给出的是摘要，但你需要原始帖子、原始视频、原始商品或原始职位结果
- AI 覆盖面不足，漏掉垂直站点信息
- 需要更高权威性或更强领域相关性
- 用户明确要求“从某个平台找”

单次查询通常控制在 1 个 AI 源 + 1 到 2 个专用源，避免结果过载。

## 处理不可用的源

当站点不可用时：

- 不要因为单个源失败而中止整个搜索
- 记录：「已跳过：<site> 不可用」
- 回退顺序按下面三层依次尝试，**不要在同一层里反复打转**：
  1. **同类垂直站点**：仅当 registry 里还有 1 个明显匹配该领域的其他站点（例如小红书失败 → 微博），最多再试 1 个，不要把整类垂直源都跑一遍。
  2. **AI 源**（grok / doubao / gemini）：仅当问题确实属于"需要综合性回答 / 跨多源舆论"的场景。事实型查询（天气、汇率、时间等）跳过这一层。
  3. **WebFetch / Bash curl 公开端点**：到了这一层就不要再回头跑 opencli。参见顶部"不适用本 skill 的场景"列出的常见公开端点。

整次问题求解中，opencli 的真实执行调用（不含预检）累计不应超过 **3 次**。达到上限仍未拿到可用结果，直接进入第 3 层 WebFetch，并在最终回答里说明 opencli 路径的失败原因。

不要假设任何站点"绝对可用"。即使是公开站点，也以当前环境中的 live help 和执行结果为准。

## 参考文件

根据需要读取对应文件：

- **`references/sources-ai.md`** — AI 默认源
- **`references/sources-tech.md`** — 技术 / 学术
- **`references/sources-social.md`** — 社交媒体
- **`references/sources-media.md`** — 媒体 / 娱乐
- **`references/sources-info.md`** — 资讯 / 知识
- **`references/sources-shopping.md`** — 购物
- **`references/sources-travel.md`** — 旅游
- **`references/sources-other.md`** — 其他垂直源

只读与当前查询相关的文件，无需全部加载。
