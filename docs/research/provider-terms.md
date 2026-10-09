# Provider terms research: using a consumer/prosumer subscription from a third-party app

Read date for every source below: **2026-10-08** unless another date is given. Research method: web search plus page fetches. Page text was returned by a fetch tool that summarizes pages, so quotes marked "verbatim" are the strings the tool returned inside quotation marks; they should be re-checked against the live page before they are cited in a legal or public context. Statements tagged **[inference]** are mine, not the provider's. Statements tagged **[secondary]** come from news, blogs or community posts, not from the provider.

This is not legal advice. Where a decision carries real risk, get written confirmation from the provider or a lawyer (see "Open questions").

Project rule being tested (from `docs/DECISIONS/0003-official-clis-only.md`): the daemon launches the provider's official CLI/SDK that the user installed and logged into, or uses the user's own API key. It never reads, copies or forwards tokens/cookies/OAuth credentials and never spoofs another client.

---

## 1. Summary matrix

| Provider | What is allowed (per sources) | Integration route for us | Color | Confidence |
|---|---|---|---|---|
| Anthropic (Claude Pro/Max) | User signing in to the **unmodified Claude Code binary** with their own subscription is explicitly allowed, including when a platform hosts it. Third-party developers may not route requests through Free/Pro/Max credentials or collect/intermediate Claude.ai credentials. Third-party harnesses that used subscription OAuth were blocked (Jan, Feb, Apr 2026). | Launch user's own `claude` (`-p`, `--output-format stream-json`), or Agent SDK / API key (Console). ACP via Claude Agent adapter exists. | **Yellow** (CLI launched by the user's own session: yellow-leaning-green; any token reuse: red) | Medium |
| OpenAI (ChatGPT Plus/Pro) | Codex CLI with ChatGPT sign-in is first-party. "Sign in with ChatGPT" plan usage is an official OAuth program for open-source tools and local personal projects (Plus/Pro). OpenAI staff publicly endorsed Codex subscriptions in third-party harnesses. | Launch `codex exec --json` or `codex app-server` (JSON-RPC, stdio) with the user's own login; or register through the official Sign in with ChatGPT program; or API key. | **Green** for official CLI and official OAuth program (local/open source); **yellow** for hosted/paid apps (waitlist) and for the ToS "automatically or programmatically" clause | Medium-high |
| Google (Gemini, Antigravity) | Consumer "Login with Google" in Gemini CLI ended 2026-06-18. Antigravity terms say using third-party software to access the service is a breach; bans were issued Feb 2026. API key (AI Studio / Vertex) is supported for third parties. | Gemini CLI only with paid API key / Standard-Enterprise; Antigravity CLI `agy` is the consumer successor (headless/ACP not confirmed); otherwise Gemini API key. | **Red** for subscription use from third parties; **green** for API key | High (for the restriction), low (for Antigravity CLI headless/ACP) |
| xAI (SuperGrok) | xAI's own Grok Build CLI is available to SuperGrok / X Premium+ subscribers, has headless `-p` and "full ACP support" for building bots and orchestration apps. xAI reportedly announced OpenCode access via SuperGrok OAuth (secondary only). | Launch `grok` CLI (`-p`, ACP) with user's own login; or xAI API key. | **Yellow-green** (official CLI/ACP is advertised for this; terms text not retrievable) | Low-medium |
| Z.ai (GLM Coding Plan) | Plan "may only be used within officially supported tools and products"; supported list includes Claude Code, Cline, OpenCode and others. General API access or own apps are not covered. | User configures supported tool (e.g. Claude Code with Z.ai endpoint) with their own plan key; for our own app use pay-per-token API. | **Yellow** (green only through listed tools; red for our own client calling the plan API) | Medium |
| Moonshot / Kimi | Kimi Code membership benefits work in Claude Code, Roo Code, OpenCode, OpenClaw, Hermes via a membership API key; Kimi CLI has ACP. Tampering with the User-Agent is a violation. Real current flagship: **Kimi K3** (confirmed by Kimi docs). | Kimi CLI (ACP) or listed tools with the user's own Kimi Code API key, genuine client identity. | **Yellow-green** | Medium |
| OpenCode / pi (as products) | OpenCode removed Claude Pro/Max auth after "legal requests" from Anthropic (merged 2026-03-19, secondary); its own docs say Anthropic "explicitly prohibits" it. It offers ChatGPT Plus/Pro (now an OpenAI partner), Copilot, xAI SuperGrok sign-in. pi supports OAuth `/login` for subscription providers. | Reference only: tells us which providers tolerate third-party clients. | n/a | Medium |
| GitHub Copilot CLI | `--acp` server documented, in public preview; Copilot SDK exists. Governing terms changed 2026-03-05, text not retrieved. | `copilot --acp` | **Yellow** | Low |
| Cursor | `agent -p ... --output-format stream-json` and `agent acp` documented; API key is the headless channel. Terms on third-party use not found. | `agent acp` | **Yellow** | Low |
| Qwen Code | Free OAuth tier ended 2026-04-15 (Qwen docs, secondary summary). CLI itself is open source and lists ACP. | API key / Alibaba Cloud Coding Plan | n/a | Medium |
| Mistral Vibe | Vibe CLI on Le Chat Pro/Team plans or BYO API key; listed in ACP agents. Terms not retrieved. | `vibe` via ACP, or API key | **Yellow** | Low |
| Amp | Subscriptions in beta; license "limited, revocable, non-exclusive, non-transferable, non-sublicensable" [secondary]. Listed as OpenAI Sign in with ChatGPT partner [secondary]. | Not a priority | **Yellow** | Low |

---

## 2. Anthropic

### 2.1 What the terms literally say

1. **Claude Code legal and compliance page** (https://code.claude.com/docs/en/legal-and-compliance, read 2026-10-08). Section "Authentication and credential use":
   - "OAuth authentication is intended exclusively for purchasers of Claude Free, Pro, Max, Team, and Enterprise subscription plans and is designed to support ordinary use of Claude Code and other native Anthropic applications."
   - "Developers building products or services that interact with Claude's capabilities, including those using the Agent SDK, should use API key authentication through Claude Console or a supported cloud provider. Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow."
   - Carve-out: "Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code as described under *Can customers offer Claude Code in their products?* above."
   - "Anthropic reserves the right to take measures to enforce these restrictions and may do so without prior notice."
   - Section "Acceptable use": "Advertised usage limits for Pro and Max plans assume ordinary, individual usage of Claude Code and the Agent SDK."
   - Section "Can customers offer Claude Code in their products?": "The Claude Code binary must not be modified. Claude Code must be installed and run as published by Anthropic, and customers may not remove, disable, or restrict any authentication method built into it" and "Customers may not pay for, resell, or intermediate Claude usage on their end users' behalf. Each end user must authenticate with their own Anthropic API key, Claude subscription plan credentials, or 3P inference provider credential". Also: you may say your product runs Claude Code, but may not use the Claude Code or Anthropic names/logos in your product, feature or company name.
2. **Consumer Terms of Service** (https://www.anthropic.com/legal/consumer-terms, effective October 8, 2025 per the fetch):
   - Section 3, item 7: users may not "Except when you are accessing our Services via an Anthropic API Key" ... "access the Services through automated or non-human means, whether through a bot, script, or otherwise." (The fetch returned this as two fragments; the full sentence should be checked on the live page.)
   - Section 2: "You may not share your Account login information, Anthropic API key, or Account credentials with anyone else."
   - Section 3 item 2 prohibits to "resell the Services" (fragment).
   - **[inference]** Read literally, the "automated or non-human means" clause is in tension with the officially documented `claude -p`; Anthropic's own docs and support article treat `claude -p` as a supported use of the subscription, so the practical reading is that Anthropic's own client doing automation is fine. No clause or statement was found that resolves this explicitly.
3. **Agent SDK overview** (https://code.claude.com/docs/en/agent-sdk/overview): "Unless previously approved, Anthropic does not allow third party developers to offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK. Use the API key authentication methods described in the Quickstart instead." Branding: "Not permitted: 'Claude Code' or 'Claude Code Agent'"; "Your product should maintain its own branding and not appear to be Claude Code or any Anthropic product."
4. **Headless docs** (https://code.claude.com/docs/en/headless): "In bare mode, Claude Code never reads OAuth credentials or the system keychain. For the Anthropic API, set `ANTHROPIC_API_KEY`..." (so `--bare` means API key only).
5. Agent SDK usage is "governed by Anthropic's Commercial Terms of Service".

### 2.2 Subscription billing for `claude -p` / SDK / third-party apps (moving target)

- Support article https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan (read 2026-10-08): banner dated June 15, 2026: "We've paused the previously-announced changes to Claude Agent SDK usage." and "For now, nothing has changed: Claude Agent SDK, `claude -p`, and third-party app usage still draw from your subscription limits." Update dated October 7, 2026: "Claude Max and Team plans now include monthly API credits," "which cover the Claude Agent SDK, claude -p, the Claude API, and Claude Managed Agents." and "You can still use the Claude Agent SDK, claude -p, and third-party apps with your subscription limits." The page does not mention ACP.
- History [secondary: Zed blog https://zed.dev/blog/anthropic-subscription-changes, VentureBeat]: on 2026-05-13 Anthropic announced that from June 15 Agent SDK / `claude -p` / ACP / third-party SDK apps would move to a separate monthly credit ($20 Pro, $100 Max 5x, $200 Max 20x); on 2026-06-16 Anthropic told subscribers the change was not taking effect and that it would give advance notice of any future change.
- **[inference]** Billing rules for programmatic use have changed three times in 2026. The daemon must not assume any fixed quota treatment and should surface "usage exhausted" errors.

### 2.3 Enforcement and blocks (dated)

| Date | Event | Source |
|---|---|---|
| 2026-01-09 (approx.) | Anthropic tightened server-side safeguards; tools spoofing the Claude Code client (OpenCode and others) got blocked. Engineer Thariq Shihipar: "tightened our safeguards against spoofing the Claude Code harness" and third-party harnesses "are prohibited by our Terms of Service" | [secondary] The Register https://www.theregister.com/2026/02/20/anthropic_clarifies_ban_third_party_claude_access/ ; HN https://news.ycombinator.com/item?id=46549823 |
| ~2026-02-19/20 | Legal/compliance page updated; The Register quotes it as "Using OAuth tokens obtained through Claude Free, Pro, or Max accounts in any other product, tool, or service" being not permitted. (The current live wording differs, see 2.1; I did not read the February version.) | [secondary] The Register |
| 2026-03-19 | OpenCode merged PR #18186 "Remove anthropic references per legal requests", removing the Anthropic OAuth plugin (OpenCode 1.3.0) | [secondary]; OpenCode docs confirm removal, see section 8 |
| 2026-04-04 | Enforcement extended to other harnesses (OpenClaw etc.); subscription kept working in Anthropic's own apps | [secondary] DEV Community, VentureBeat |
| 2026-05-13 / 06-15 / 06-16 | Agent SDK credit announced, then paused | see 2.2 |

### 2.4 Official integration routes

- Headless CLI: `claude -p "prompt" --output-format stream-json --verbose --include-partial-messages`; `--output-format json`; `--resume <session_id>`, `--continue`; `--allowedTools`; `--permission-mode`; `--permission-prompts none`; `--bare` (API key only); `--max-budget-usd`; SIGINT to end a turn cleanly. Docs: https://code.claude.com/docs/en/headless. SDK input stream: `--input-format stream-json` (flag exists in CLI reference, not re-verified here).
- Agent SDK (Python/TypeScript): runs the Claude Code binary; requires API key (or Bedrock/Vertex/Foundry) for products offered to others: https://code.claude.com/docs/en/agent-sdk/overview.
- ACP: ACP agents page lists "Claude Agent" "Works through Zed's SDK adapter" (https://agentclientprotocol.com/get-started/agents). Whether that adapter may use a subscription login is the same unresolved question as the Agent SDK note above.
- MCP: first-class (`--mcp-config`, SDK MCP support).
- API key: Claude Console.

### 2.5 Parallel agents on one subscription

- Plan limits are shared across surfaces; "Advertised usage limits for Pro and Max plans assume ordinary, individual usage of Claude Code and the Agent SDK." No documented concurrency number was found. **[inference]** Many parallel `claude -p` processes on one Pro/Max login risk the "ordinary, individual usage" language and rate limits; cap parallelism and let the user choose.

### 2.6 Assessment

**Yellow, medium confidence.**
- Green part: the user installs and logs into the unmodified `claude` binary and our daemon only starts it as a child process; the compliance page expressly says a user may sign in to the unmodified binary with their own subscription, and `claude -p` is documented and (per the support page) still draws on subscription limits.
- Yellow reasons: the Agent SDK note ("Unless previously approved ... offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK"); the consumer-terms "automated or non-human means" clause; the phrase "route requests through Free, Pro, or Max plan credentials on behalf of their users" could be read against a daemon that fronts the CLI for other users; the support page says "third-party apps" use subscription limits but that is billing, not a license.
- Red: anything that reads/reuses OAuth tokens, modifies the binary, or presents a Claude.ai login in our UI.
- What would change it: written confirmation from Anthropic (sales/support) that a local app spawning the user's own `claude` is allowed; a new revision of the legal page; the SDK note being updated.

---

## 3. OpenAI

### 3.1 What the terms literally say

- **Terms of Use**: https://openai.com/policies/terms-of-use/ returned HTTP 403 to my fetch. I could not read it directly. Via search-result snippets (secondary mirrors, not verified against the live page): the "What you cannot do" list includes "automatically or programmatically extract data or Output", and the Registration section says "You may not share your account credentials or make your account available to anyone else and are responsible for all activities that occur under your account." **Not verified verbatim; re-check on openai.com.**
- **Codex GitHub discussion #8338** (https://github.com/openai/codex/discussions/8338): OpenAI maintainer etraut-openai, Dec 19, 2025: "The codex CLI sources are licensed under a permissive Apache license, and you're welcome to fork the repo"; Feb 9, 2026: "our terms of use and code license are quite permissive." Informal, not policy.
- **Codex auth docs** (https://learn.chatgpt.com/docs/auth, redirected from developers.openai.com/codex/auth): "Codex supports two ways for a person to sign in when using OpenAI models" (ChatGPT subscription or API key); "Codex caches login details locally in a plaintext file at `~/.codex/auth.json` or in your OS-specific credential store."; "treat `~/.codex/auth.json` like a password: it contains access tokens."; "Use API key authentication for programmatic Codex CLI workflows, such as CI/CD jobs." and "API keys are still the recommended default for automation."
- **Sign in with ChatGPT for open-source apps** (https://developers.openai.com/cookbook/articles/sign-in-with-chatgpt): "The ChatGPT plan usage integration described here is available for open-source tools and personal projects that run locally."; "If you're building a paid or remotely hosted app, join the waitlist to request access before offering it to users."; "Identity-only sign-in is limited to 'a select group of commercial partners.'"; "a successful identity sign-in alone does not grant permission to use the user's ChatGPT plan."; "Eligible ChatGPT Plus and Pro users can try your tool."; users manage app access and limits in ChatGPT settings. Preview limits reported [secondary]: `stream=true`, `store=false`, some hosted tools excluded.

### 3.2 Official integration routes

- Non-interactive: `codex exec "prompt"`; `--json` for JSONL events; `--ephemeral`; `--sandbox workspace-write`; `codex exec resume --last`. Docs: https://developers.openai.com/codex/noninteractive . (Default sandbox read-only: sources disagree; verify with `codex exec --help`.)
- App server: https://developers.openai.com/codex/app-server : "Codex app-server is the interface Codex uses to power rich clients (for example, the Codex VS Code extension)." and "Use it when you want a deep integration inside your own product". JSON-RPC 2.0 over stdio (default); "WebSocket transport is experimental and unsupported"; "The app-server command and WebSocket transport are experimental and aren't supported for production workloads."; "If you are developing a new Codex integration intended for enterprise use, please contact OpenAI to get it added to a known clients list." I did not read the part of the page describing its login methods (page truncated).
- Codex SDK: https://learn.chatgpt.com/docs/codex-sdk : "Programmatically control local Codex agents."; TypeScript library "start, continue, and resume local Codex threads"; Python SDK "controls the local Codex app-server over JSON-RPC". No statement on plan eligibility.
- ACP: Codex is listed in the ACP agents page, "Works through ACP's adapter" (adapter `codex-acp`, moved to `@agentclientprotocol/codex-acp`, built on the app server [secondary]). Zed offers "ChatGPT Login" as one of three auth options [secondary].
- Official OAuth program: Sign in with ChatGPT (above). Launch partners reported [secondary: The New Stack https://thenewstack.io/sign-in-with-chatgpt/, others]: OpenCode, Devin, Amp, Warp, Notion, Vercel and others; weekly per-app caps set by the user.
- API key: standard.

### 3.3 Enforcement

None found against third-party Codex-subscription clients. OpenAI's Thibault Sottiaux publicly endorsed using Codex subscriptions in third-party harnesses [secondary: The Register article above]. A September 2026 GitHub issue (ReadyPlayerTwo #29) claimed no supported flow existed; **[inference]** that claim predates or ignores the DevDay program.

### 3.4 Parallel agents

No concurrency number found. Plan allowance is shared; users can cap per app. `codex exec` with CI guidance recommends API keys.

### 3.5 Assessment

**Green for launching the user's own `codex` (exec / app-server / ACP adapter) and for the official Sign in with ChatGPT program (local or open-source apps); yellow for hosted/paid distribution (waitlist) and until the Terms of Use are read verbatim. Confidence medium-high.** Changes it: a terms revision restricting programmatic use; the app-server/SDK pages stating plan exclusions; our project being "remotely hosted/paid" (then the waitlist applies).

Do not read or copy `~/.codex/auth.json` (the OpenAI docs themselves say it contains access tokens; copying it to a headless machine is mentioned as a fallback in their docs, but our rule 0003 forbids touching it; use device-code login on the target machine instead).

---

## 4. Google (Gemini CLI, Antigravity)

### 4.1 What the terms say

- **Gemini CLI terms/privacy doc** (https://github.com/google-gemini/gemini-cli/blob/main/docs/resources/tos-privacy.md): "Supported authentication methods include:" "Logging in with your Google account to Gemini Code Assist.", "Using an API key with Gemini Developer API.", "Using an API key with Vertex AI GenAI API." Directly accessing the services powering Gemini CLI using "third-party software, tools, or services" "is a violation of applicable terms and policies" and "may be grounds for suspension or termination of your account." (Fragments as returned by the fetch.)
- **Antigravity Additional Terms** (https://antigravity.google/terms, Section 6, no effective date shown): "Using third party software, tools, or services to access the Service" (e.g. using OpenClaw with Antigravity OAuth) is a breach of this Agreement; "Such actions may be grounds for suspension or termination of your Antigravity and/or Gemini CLI accounts."
- **Consumer deprecation** (https://developers.google.com/gemini-code-assist/docs/deprecations/code-assist-individuals): "Starting June 18, 2026, Gemini Code Assist IDE extensions stopped serving requests" for "the Gemini Code Assist for individuals, Google AI Pro, and Google AI Ultra tiers"; "you can no longer use the Login with Google option to access the IDE extensions or Gemini CLI"; "Gemini Code Assist Standard or Enterprise subscriptions remain unchanged". Blog (https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/, May 19, 2026): Gemini CLI "will remain accessible via paid Gemini and Gemini Enterprise Agent Platform API keys"; Antigravity CLI is the consumer replacement. The blog does not mention headless mode, ACP or SDK, and does not address third-party tools.

### 4.2 Enforcement

- Maintainer post, GitHub google-gemini/gemini-cli Discussion #20632, Feb 27, 2026 (jackwotherspoon): bans addressed "violations of the Antigravity Terms of Service" through third-party tools or proxies; bans also hit Gemini CLI and Code Assist; "All currently affected accounts should see their access restored in a day or two."; flagged users get an email and form to recertify; second violation "will be permanently banned." Later user reports (May 2026) of unanswered appeals and bans for users who say they only used an API key [secondary, anecdotal; Google's framing is OAuth piggybacking].

### 4.3 Integration routes

- Compliant: Gemini API key (AI Studio) or Vertex AI for any third-party app; Gemini CLI with a paid API key or Standard/Enterprise license (headless `-p` and ACP exist in Gemini CLI; ACP agents page lists Gemini CLI; flags not re-verified).
- Antigravity CLI (`agy`) and Antigravity SDK exist (Google blog; details from third-party guides [secondary]). Whether `agy` has a documented headless/ACP mode and whether a third-party UI launching it is permitted was **not established**; the Antigravity Terms Section 6 wording is the risk.

### 4.4 Assessment

**Red for using a Google AI Pro/Ultra subscription from any third-party client; green for Gemini API key / Vertex. High confidence on the restriction.** Antigravity CLI launched by our daemon with the user's own session: unknown (treat as yellow-red until Google clarifies). Parallel-agent limits: not researched; Antigravity quota is on its pricing page.

---

## 5. xAI

### 5.1 What the terms say

- xAI Terms of Service pages (https://x.ai/terms-of-service and https://x.ai/legal/terms-of-service) returned HTTP 403 to my fetch. Via search snippets of older consumer versions (June 4 and Dec 20, 2024; not the current version): users may not "use any robot, spider, scraper, off-line reader, data mining tool, data gathering or extraction tool, or any other automated means to access the Service" **[secondary snippet, older version, not verified against current text]**. The 2025/2026 consumer versions reportedly say the "Enterprise Terms of Service govern the use of our services for developers and businesses, including xAI APIs." A consumer-terms version dated April 10, 2026 exists (listed as previous). **I could not read the current live terms.**

### 5.2 Official products and routes

- Grok Build announcement (https://x.ai/news/grok-build-cli, dated May 25, 2026): "Now in early beta for all SuperGrok and X Premium Plus subscribers."; "Install Grok Build with a single command and sign in with your account."; "Headless mode (`-p`) allows easily running agents inside scripts and automations."; "Grok Build also provides full ACP support to build your own bots and agent orchestration apps."; AGENTS.md, plugins, hooks, skills, MCP work out of the box. This is the strongest primary evidence of any provider that a subscription CLI is intended as a backend for third-party UIs.
- OpenCode: reportedly announced on 2026-05-21 that SuperGrok/X Premium subscribers can use Grok in OpenCode via `/connect` OAuth [secondary: https://cryptobriefing.com/xai-grok-opencode-coding-integration/ , no direct xAI quote]; OpenCode docs say: "Two ways to authenticate: a SuperGrok subscription via device-code OAuth or a pay-as-you-go API key from the xAI console." and "Any Grok or X Premium plan that includes Grok API access works." Community reports of HTTP 403 for standard SuperGrok subscribers on OAuth API endpoints [secondary].
- ACP registry has `grok-build` [secondary].
- API key: xAI console (OpenAI-compatible API).

### 5.3 Enforcement

None found.

### 5.4 Assessment

**Yellow-green, low-medium confidence.** Launching the official `grok` CLI headlessly (`-p`) or over ACP is advertised by xAI. Not verified: current ToS automation clause, plan tier restrictions (one Japanese blog says SuperGrok Lite/X Premium differ [secondary]), concurrency. Changes it: reading the current ToS; xAI docs on OAuth for third parties.

---

## 6. Z.ai (GLM Coding Plan)

- Usage policy (https://docs.z.ai/devpack/usage-policy): "GLM Coding Plan may only be used within officially supported tools and products."; "Subscription benefits are exclusive to the subscriber."; "Account sharing or multi-user access is prohibited."; "Violations of the Usage Rules may trigger risk control measures, including rate limiting, account freezing"; "Accounts with more than three violations may be banned."; "Rate (concurrency) limits are tied to your plan tier."; "The platform dynamically adjusts these limits based on resource availability"; recommended use Lite one project, Pro one to two, Max two or more simultaneous projects.
- Overview (https://docs.z.ai/devpack/overview): "Beyond Claude Code, it also supports Cline, OpenCode, and some specific tools."; "Each plan is subject to both a 5-hour usage limit and a weekly usage limit." Credits: Lite 2,000 / 10,000; Pro 12,000 / 60,000; Max 28,000 / 140,000 (5-hour / weekly).
- Additional rules from a search summary of the subscription terms [secondary, https://docs.z.ai/legal-agreement/subscription-terms not fetched]: no general-purpose API access "such as calling models from your own apps, bots, or SaaS products" without a separate written agreement; no reselling/proxying. Z.ai's subscribe page lists Claude Code, Codex, ZCode, Kilo Code, Cline, OpenCode, OpenClaw and more [secondary]. A GitHub issue (zai-org/zai-coding-plugins #24) asks how to get a tool added; no official process found.
- Route: Claude Code via Z.ai's Anthropic-compatible endpoint with the user's plan API key (endpoint URL not on the overview page; take from Z.ai docs when implementing).
- Assessment: **Yellow, medium confidence.** If the user runs a supported tool (Claude Code or OpenCode) configured by them, our daemon launching that tool is consistent with the policy. **[inference]** Our own client calling the plan endpoint directly is outside "officially supported tools". Parallelism: Pro supports 1-2 simultaneous projects per Z.ai's guidance.

---

## 7. Moonshot / Kimi

- **Model name check**: Kimi docs (https://www.kimi.com/code/docs/en/) say "Powered by our strongest flagship model K3 — 2.8 trillion parameters." Model IDs: `k3`, `k3-256k`, `kimi-for-coding` (K2.8 Preview), `kimi-for-coding-highspeed` (K2.7 Code HighSpeed). So "Kimi K3" is real (release 16 July 2026 per [secondary]); API docs list kimi-k3 (https://platform.kimi.ai/docs/guide/kimi-k3-quickstart).
- Third-party use (https://www.kimi.com/en/help/kimi-code/third-party-agents): "Kimi Code benefits support use in mainstream Coding Agents, such as Claude Code, Roo Code, and OpenCode."; "You can also use them with general Agent frameworks such as OpenClaw and Hermes"; "When using third-party tools, please keep the tool's genuine identity;" tampering with the User-Agent "will be treated as a violation and may result in suspension of your membership benefits."
- Membership guide (https://www.kimi.com/en/help/kimi-code/membership-guide): "Subscribers can connect to third-party development tools such as Claude Code, Roo Code, and OpenCode via an API Key."; "maximum concurrency of 30"; "about 300–1,200 requests every 5 hours"; up to 5 API keys per member. The page does not address personal-use-only, automation or resale. A third-party site claims membership keys are for personal interactive use only and not automation/resale [secondary, unverified against Kimi terms]; Kimi Terms of Services (https://www.kimi.com/user/agreement/modeluse?version=v2) were not read.
- Kimi CLI: ACP supported; docs name "JetBrains and Zed" as editors connecting via "CLI's ACP protocol"; CLI "moving from Python/uv to Node.js". Kimi CLI is in the ACP agents list.
- Plan names/prices are in flux (September 2026 reports of renamed tiers, secondary).
- Assessment: **Yellow-green, medium confidence.** Official statements welcome third-party tools through a Kimi Code API key and ACP. Unverified: terms on automation/resale. Concurrency 30 is a documented limit.

---

## 8. OpenCode (sst/opencode) and pi

### OpenCode
- Providers doc (https://opencode.ai/docs/providers/): Anthropic: "There are plugins that allow you to use your Claude Pro/Max models with OpenCode. Anthropic explicitly prohibits this." (bundled plugins removed as of 1.3.0). OpenAI: "Here you can select the ChatGPT Plus/Pro option and it'll open your browser and ask you to authenticate." GitHub Copilot: sign-in via device code. xAI: SuperGrok via device-code OAuth or API key.
- History: Anthropic-related removal commit "anthropic legal requests" (~2026-02-19) and PR #18186 (2026-03-19) [secondary]. Community plugins still exist for Claude OAuth; they are third-party and exactly what Anthropic prohibits.
- Implication for us: OpenCode is tolerated by OpenAI (named launch partner [secondary]) and xAI, forced out by Anthropic, and excluded by Google. A third-party client's treatment is per provider, and changes within months.

### pi (Mario Zechner)
- Verified identity [secondary: unpkg README, taoofmac]: pi-mono by Mario Zechner (badlogic); in April 2026 he joined Earendil (with Armin Ronacher); repo moved to `earendil-works/pi`; package moving from `@mariozechner/pi-coding-agent` to `@earendil-works/pi-coding-agent`. MIT core.
- Its providers doc (https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/providers.md): supports browser/device OAuth `/login` and API keys; `auth.json` "can contain API keys and OAuth tokens"; lists Anthropic, OpenAI, Google Gemini, GitHub Copilot, xAI, ZAI Coding Plan, Kimi For Coding, Moonshot and others. The page contains no ToS warnings and no mention of Claude Pro/Max or ChatGPT subscription logins in the version the fetch returned. Pi is in the ACP agents list via the `pi-acp` adapter. No evidence found of providers objecting to pi specifically.
- Implication: pi/OpenCode patterns that store or reuse OAuth tokens are the model we must not copy (rule 0003).

---

## 9. Briefly: other CLIs

- **GitHub Copilot CLI**: `--acp` starts an ACP server ("allows you to use Copilot CLI as an agent in any third-party tools, IDEs, or automation systems"), public preview [secondary: Microsoft Tech Community, daily.dev]; Copilot SDK exists, needs a Copilot subscription unless BYOK [secondary]; billing by premium requests. Copilot Product Specific Terms deprecated 2026-03-05, replaced by GitHub Generative AI Services Terms, text not read. CLI license: "non‐exclusive, non‐transferable, royalty‐free license to install and run copies" [secondary snippet]. Yellow.
- **Cursor**: `agent -p "..." --output-format stream-json` and `agent acp` documented (https://cursor.com/docs/cli/overview); API key is the headless/CI channel; terms for third-party UI use not found. Yellow.
- **Qwen Code**: free OAuth tier ended 2026-04-15 (https://qwenlm.github.io/qwen-code-docs/en/users/configuration/auth/ via search summary); ACP-listed. Use API key / Alibaba Cloud Coding Plan.
- **Mistral Vibe**: Vibe CLI included in Le Chat (now Vibe) Pro/Team plans or BYO API key (https://mistral.ai/news/mistral-vibe-2-0/, secondary summary); ACP-listed. Terms not read.
- **Amp**: license "limited, revocable, non-exclusive, non-transferable, non-sublicensable" (https://ampcode.com/terms, search snippet); subscriptions in beta (https://ampcode.com/news/subscriptions); no SDK terms found.

---

## 10. Protocols that make third-party frontends legitimate

| Provider | Headless/JSON stream | Agent SDK | ACP | MCP | Notes |
|---|---|---|---|---|---|
| Anthropic | `claude -p --output-format stream-json` (official) | Python/TS (API key for products offered to others) | via Zed SDK adapter ("Claude Agent") | yes | SDK note forbids offering claude.ai login |
| OpenAI | `codex exec --json` | Codex SDK (TS/Python, local agents) | via `codex-acp` adapter | yes (Codex as client and server, not re-verified) | `app-server` JSON-RPC for rich clients; experimental/unsupported for production per OpenAI docs; official Sign in with ChatGPT OAuth program |
| Google | Gemini CLI `-p` (not re-verified); `agy` unknown | Antigravity SDK (secondary) | Gemini CLI listed in ACP | yes | subscription path closed to third parties |
| xAI | `grok -p` (official) | not found | "full ACP support" (official) | yes (official) | |
| Z.ai | via supported tools only | no | via tools | via tools | |
| Kimi | Kimi CLI | not found | Kimi CLI ACP (official docs) | yes (secondary) | |
| GitHub Copilot | CLI | Copilot SDK | `--acp` (preview) | yes | |
| Cursor | `agent -p` | n/a | `agent acp` | yes | |

ACP (https://agentclientprotocol.com) lists 41 agents on 2026-10-08 including Claude Agent, Codex CLI, Gemini CLI, GitHub Copilot (preview), Kimi CLI, Mistral Vibe, OpenCode, Pi, Qwen Code, Cursor, Cline. A registry (https://github.com/agentclientprotocol/registry) has more (grok-build per secondary source). **ACP legitimizes the protocol, not the authentication**: each provider's terms still govern whose credentials are used.

---

## 11. Compliant integration architecture (what our daemon can safely do)

1. **Process boundary**: spawn the official binary as the same OS user, inherit the user's own login state, never open its credential files, never pass tokens in env or args, never rewrite User-Agent/headers, never patch or wrap the binary (Anthropic: "must not be modified"; Kimi: keep genuine identity). Only `internal/provider` spawns.
2. **Per provider**
   - Anthropic: `claude -p --output-format stream-json` on the user's own login (yellow; show a one-time notice and let the user choose API key mode). For API-key mode use Agent SDK or `--bare` with `ANTHROPIC_API_KEY` from the OS credential store. Do not offer "Sign in with Claude" in our UI; direct the user to run `claude` login themselves. Do not use the names "Claude Code" in our product name.
   - OpenAI: `codex exec --json` or `codex app-server` (stdio) on the user's own login; or register through Sign in with ChatGPT (local/open-source). API key fallback. Do not read `auth.json`.
   - Google: API key (AI Studio/Vertex) only by default. Treat Gemini CLI Login-with-Google and Antigravity as unsupported until Google confirms.
   - xAI: `grok -p` / ACP on user's own login; API key fallback.
   - Z.ai and Kimi: launch the user's configured supported tool (Claude Code with their endpoint, Kimi CLI over ACP) or accept an API key stored in the OS credential store and configure only the official tool's environment. For our own direct HTTP calls use the pay-per-token API.
3. **Capability flags per adapter**: `subscription_ok`, `api_key_ok`, `acp`, `max_parallel`, `terms_checked_at` (date), so a changed rule disables one provider without touching others (matches ADR 0003).
4. **Parallelism**: default concurrency per subscription = 1-2; configurable; Kimi documented max 30; Z.ai Pro 1-2 projects. Back off on rate-limit errors (Claude emits `system/api_retry` events with `rate_limit` category).
5. **Single-user**: one subscription per human; no pooling, rotation, sharing across users, or exposing the daemon to other people as a service (Anthropic "individual usage"; Z.ai "Account sharing or multi-user access is prohibited"; OpenAI/Anthropic account-sharing clauses).
6. **Telemetry/logs**: never log prompts with credentials; never log environment of child processes.
7. **UI text**: say "runs your installed Claude Code / Codex"; do not imply endorsement; follow Anthropic branding guidance.

---

## 12. Open questions

1. Anthropic: does a local app that spawns the user's own `claude -p` fall under "ordinary, individual usage"? Does the Agent SDK note ("Unless previously approved...") apply to it? Ask Anthropic sales/support for written confirmation. Also read the full Consumer Terms Section 3 item 7 sentence on the live page.
2. Anthropic: what did the February 2026 version of the legal page say exactly (Internet Archive), and has "ACP via Claude Agent adapter with subscription" been addressed?
3. OpenAI: read the live Terms of Use (403 for my fetch) and the part of the app-server doc covering login methods (`chatgpt` vs `chatgptAuthTokens`); confirm whether an app-server client launched by our daemon with the user's ChatGPT login is within "known clients" expectations; decide whether to apply to Sign in with ChatGPT (and whether our distribution counts as "remotely hosted").
4. Google: does Antigravity CLI (`agy`) offer headless/ACP, and does Section 6 of the Antigravity terms allow a local third-party UI to launch it? Ask Google.
5. xAI: current ToS text (403), whether SuperGrok subscription use through third-party OAuth is blessed, tier restrictions.
6. Kimi: read the Kimi Paid Services Agreement for automation and resale rules; confirm the personal-use claim.
7. Z.ai: read the full subscription terms and the supported-tools list; ask whether a local daemon launching Claude Code counts as supported use.
8. Copilot and Cursor: read current GitHub Generative AI Services Terms and Cursor terms for third-party ACP clients.
9. Legal review before public launch, especially for any hosted/VPS multi-user scenario (outside what these terms contemplate).
10. Rate-limit behavior under real parallel load per provider: measure rather than assume.

---

## 13. Verification log (2026-10-09)

Re-checked on the live pages by fetching them again (the fetch tool still summarizes, but these passages were returned as quoted blocks):

- **Anthropic, https://code.claude.com/docs/en/legal-and-compliance (verified, matches section 2.1):** "Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens"; and "Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription". Also verified: "The Claude Code binary must not be modified. Claude Code must be installed and run as published by Anthropic"; "Customers may not pay for, resell, or intermediate Claude usage on their end users' behalf"; and the branding rule: you "can accurately say, in plain text, that your product ... runs Claude Code" but "can't use the Claude Code or Anthropic names or logos as part of your own product, feature, or company name". The page shows no date.
- **OpenAI, https://learn.chatgpt.com/docs/auth (redirect from developers.openai.com/codex/auth):** confirms the two sign-in methods ("Sign in with ChatGPT for subscription access" and "Sign in with an API key for usage-based access") and "API keys are still the recommended default for automation." The page does not address third-party apps using a ChatGPT plan, so the green rating for the official CLI rests on it being OpenAI's own client; the OpenAI Terms of Use remain **unverified** (HTTP 403).
- **Not re-verified:** xAI terms (HTTP 403), Google Antigravity terms, Z.ai, Kimi, GitHub Copilot, Cursor, Mistral, Amp. Everything said about them stays labeled by the confidence in sections 1 to 9.

### Consequences for the product (inference, drawn from the verified Anthropic text)

1. A local app that starts the user's own unmodified `claude` on the user's own PC, with the user's own login, is the case the carve-out describes. Anything that reads tokens, edits the binary, hides or replaces its login, or offers a Claude.ai login inside Aotus is red.
2. Do not put "Claude", "Claude Code" or "Anthropic" in Aotus names, logos or feature names. Plain-text statements such as "works with Claude Code" are allowed.
3. The consumer-terms clause on "automated or non-human means" and the Agent SDK note keep headless use (`claude -p`) yellow. The `terminal` mode (the official interactive UI hosted in a pseudo-terminal) avoids that ambiguity.
4. Written confirmation from Anthropic that a local app may spawn the user's own `claude` in headless mode is still outstanding.

Decision: (drafted 2026-10-09 from the owner's brief and this research; the owner confirms or edits it) The MVP enables Claude Code and Codex CLI, each launched as the unmodified official binary in the user's own session on their own PC, one independent process per employee session, one profile per subscription with its own isolated configuration directory, never reading credentials, never rotating or pooling profiles; Codex CLI defaults to `structured` mode and Claude Code defaults to `terminal` mode, with Claude's `structured` mode (`claude -p`) available per profile behind a visible notice until Anthropic confirms headless use in writing; xAI Grok CLI and the other ACP CLIs wait until their terms are verified; Gemini and Antigravity subscriptions are excluded (API key only); Z.ai GLM and Kimi are reached through their supported tools or an OpenAI-compatible API key; the user's own API key through the OpenAI-compatible adapter is the fallback for every provider; and the product never uses the Anthropic or Claude Code names or logos in its own branding.
