# Incidently

AI-powered Slack bot for incident analysis and system investigation. Connects to your observability stack via MCP servers.

## What It Does

A Slack bot that runs a multi-agent system (powered by Gemini via Google ADK Go) to investigate system issues through your existing observability tools. Connects to any tool that exposes an MCP server — Grafana, Datadog, GCP Logging, Elasticsearch, PagerDuty, or anything else. Operates in human-in-the-loop mode — the operator describes a problem or asks a question in natural language, and the bot investigates using the available tools and knowledge from playbooks.

No commands. No rigid syntax. The operator just talks to the bot:

- `@bot problems with apple pay` — investigates a specific issue
- `@bot how's the system doing?` — broad health overview
- `@bot latency spike on checkout service` — focused investigation
- `@bot compare error rates to yesterday` — ad-hoc analysis

Three layers of configuration — no code changes needed to adapt to a different stack:

- **Playbooks** define domain knowledge (workflows, steps, concrete queries, dashboard names)
- **Agent instructions** define HOW each agent behaves (personality, analysis approach, response format)
- **Agent config** defines infrastructure per agent (model, temperature, which MCP tools)

The Go code is generic. It reads config, builds agents, connects to MCP servers, and runs the loop. All domain knowledge lives in playbooks and agent instructions.

## Stack

- **Go** — primary language
- **Google ADK Go** (`google.golang.org/adk`) — multi-agent framework with built-in MCP toolset, ReAct loop, agent orchestration, and session management
- **slack-go/slack** — Slack bot (Socket Mode)
- **ADK in-memory sessions** — conversation state per thread (Redis later via ADK's session/database)

## Architecture

```
Slack (operator)
    ↓
Slack Gateway (socket mode, @bot mentions, threads)
    ↓
ADK Runner (session per thread)
    ↓
Coordinator Agent (fast model, get_playbook tool, no MCP tools)
    ├── has playbook index in instructions
    ├── loads relevant playbooks on demand via get_playbook
    ├── understands operator's request
    ├── picks relevant steps from loaded playbooks
    ├── delegates to specialist agents
    ├── aggregates results
    │
    ├── Agent A (MCP tools from config)
    ├── Agent B (MCP tools from config)
    └── ... (all defined in config.yaml, not code)
    ↓
Slack Gateway (reply in thread)
```

### Components

**Slack Gateway** — receives messages via Socket Mode. Listens for `@bot` mentions. Always replies in thread. Sends progress messages during analysis.

**ADK Runner** — manages agent execution within sessions. Each Slack thread = one ADK session with its own conversation history. Uses ADK's `session.InMemoryService()`.

**Coordinator Agent** — the orchestrator. Uses a fast/cheap model. Has no MCP tools itself. Has a playbook index (name + description + tags for each playbook) in its instructions and a `get_playbook` tool to load full playbook content on demand. When the operator asks something, the coordinator matches the request against the index, loads only the relevant playbooks, picks the right steps, delegates to specialists, and aggregates results. This two-phase approach scales to dozens of playbooks without bloating the context.

**Specialist Agents** — defined entirely in config. Each has its own model, temperature, behavioral instructions, and scoped set of MCP tools. They receive tasks from the coordinator, execute them using their tools, and return structured findings. They don't know about playbooks — they just do what the coordinator asks.

**MCP Toolset** — ADK's built-in `McpToolset` connects to external MCP servers via SSE, discovers available tools, and makes them callable by agents. Each specialist is configured with only the MCP servers relevant to its role.

**Playbooks** — markdown files that serve as a knowledge base for the coordinator. They describe analysis workflows, reference concrete dashboard names and queries, and define output formats. The coordinator sees only the playbook index at first and loads full content on demand via the `get_playbook` tool — keeping context small even with dozens of playbooks.

**Agent Instructions** — markdown files describing each agent's behavior and response format. Separate from playbooks. Loaded as the agent's system instructions.

## Package Structure

```
cmd/bot/main.go           — entrypoint, wiring
internal/
  config/config.go        — config loading, env var resolution
  slack/gateway.go        — socket mode, message handling, threading
  agent/
    agent.go              — multi-agent setup, runner, session management
    playbook.go           — playbook loader (YAML frontmatter + markdown)
instructions/             — agent behavioral instructions (HOW to behave)
playbooks/                — domain knowledge (WHAT to do, WHAT to check)
config/
  config.yaml             — slack, MCP servers, agent definitions
```

## Multi-Agent Design

### Three Layers

**Playbooks** = domain knowledge. Analysis workflows with concrete steps, dashboard names, log queries, thresholds. The coordinator uses them as a knowledge base — picking relevant parts based on what the operator asks. Portable — change these when your system changes.

**Agent instructions** = behavior. General behavioral guidelines per agent role — analysis approach, response format, error handling. Reusable across any request.

**Agent config** = infrastructure. Model, temperature, which MCP tools. Lives in `config.yaml`.

### How It Works

1. Operator sends `@bot problems with apple pay`
2. Slack gateway parses the message, sends progress indicator to the thread
3. ADK runner finds (or creates) the session for this thread
4. Coordinator agent receives the message
5. Coordinator reads its playbook index (name + description + tags for each playbook). It matches "problems with apple pay" to relevant playbooks — e.g., `payment-investigation` (tags: payments, apple-pay) and `dependency-analysis` (tags: external-services, gateways)
6. Coordinator calls `get_playbook("payment-investigation")` and `get_playbook("dependency-analysis")` to load their full content. Skips irrelevant playbooks entirely (e.g., infrastructure, deployment)
7. Coordinator picks relevant steps from the loaded playbooks and delegates each to the right specialist agent
8. Specialist agents execute tasks using their MCP tools, return findings
9. Coordinator aggregates findings into a focused summary about the Apple Pay issue
10. Summary posted to Slack thread
11. Follow-up messages in the thread have full conversation context — the coordinator already has the loaded playbooks in context and can drill deeper, load additional playbooks, or pivot

### Agent Definitions

The coordinator is configured separately — the engine knows it's special and automatically gives it the playbook index, `get_playbook` tool, and all specialist agents as sub-agents. Specialist agents are defined in the `agents` list.

```yaml
coordinator:
  model: gemini-2.0-flash
  description: "Understands operator requests, picks relevant playbooks, delegates to specialists, aggregates results"
  instruction: "instructions/coordinator.md"
  temperature: 0.1

# Everything below is an example — define whatever agents your stack needs

agents:
  - name: metrics-analyst
    model: gemini-2.5-pro
    description: "Queries dashboards and interprets metrics data"
    instruction: "instructions/metrics-analyst.md"
    temperature: 0.2
    tools: [grafana]

  - name: log-analyst
    model: gemini-2.5-pro
    description: "Searches and analyzes application logs"
    instruction: "instructions/log-analyst.md"
    temperature: 0.2
    tools: [gcp-logging]
```

To add a new specialist: add a YAML block, write an instruction file, point it to the right MCP servers. No Go code changes.

### Playbook Selection

The coordinator does not have all playbooks in its context. Instead, it uses a two-phase approach:

**Phase 1 — Index matching.** The coordinator's instructions include a playbook index: name + description + tags for each playbook. This is small (~50 tokens per playbook) and scales to dozens of playbooks. The coordinator reads the operator's request and matches it against the index to identify relevant playbooks.

**Phase 2 — On-demand loading.** The coordinator calls the `get_playbook(name)` tool to load the full content of only the playbooks it needs. This is a custom function tool built into the Go engine — not an MCP tool. Only relevant playbook content enters the context.

This means: with 30 playbooks, the coordinator sees ~1500 tokens of index. A focused request like "problems with apple pay" loads maybe 1-2 playbooks. A broad request like "how's the system doing?" might load 3-4. The context stays lean.

### Playbook Format

Playbooks have YAML frontmatter (description + tags) and markdown body. They reference concrete identifiers (dashboard names, log queries, panel names). Tags help the coordinator match requests to playbooks without reading full content.

```markdown
---
description: "Payment service analysis — success rates, gateway errors, transaction logs"
tags: [payments, apple-pay, google-pay, checkout, transactions]
---

# Payment Investigation

## Payment Metrics

Query the "Payments" dashboard. Pull these panels for the last 15 minutes:
- Success rate per payment method (Apple Pay, Google Pay, Card, etc.)
- Latency p99 per payment method
- Transaction volume

Compare to the same time window last week.
Flag as warning if success rate drops > 2%, critical if > 10%.

## Payment Logs

Search payment service logs for the last 15 minutes.
Filter: service=payment-service, severity >= WARN.

Identify:
- Top error messages by payment method
- Gateway timeout patterns
- Affected endpoints

## Gateway Health

Query the "Payment Gateways" dashboard. Check:
- Gateway response times per provider
- Gateway error rates per provider
- Circuit breaker status

## Output format

Present results grouped by payment method / gateway.
Highlight which provider is affected and which are healthy.
Suggest next steps (check provider status page, check upstream, etc.).
```

### Agent Instruction Format

Agent instructions describe behavior, not workflow. They are independent of any specific playbook or observability tool:

```markdown
# Metrics Analyst

You are a professional metrics analyst.

## How you work

- Use the available tools to query the dashboards and panels requested.
- Compare current values to the baseline period specified.
  If no baseline specified, use the same time window one week ago.
- Classify: normal, warning (>20% deviation), critical (>50%).
- Distinguish system errors from client errors.
- Account for expected patterns (lower weekend/night traffic).

## Response format

For each metric checked:
- Metric name, current value, baseline value
- Status (normal / warning / critical)
- Brief note if anomalous
```

### Key Principles

**No commands.** The bot understands natural language. The operator describes a problem or asks a question — the coordinator figures out what to investigate.

**Playbooks are a knowledge base, not a rigid script.** The coordinator sees only the index, loads what it needs via `get_playbook`, and draws from them selectively. "Problems with Apple Pay" loads the payment playbook, not the infrastructure playbook.

**Anomaly ≠ Incident.** Agent instructions tell each agent how to distinguish real problems from expected noise.

**Dynamic baseline.** No hardcoded thresholds. Compare to same weekday last week.

**Concrete identifiers.** Playbooks contain exact dashboard names, panel names, log queries. The agent does not guess.

**Scoped tools.** Each specialist agent only sees the MCP tools relevant to its role.

**Generic engine.** The Go code knows nothing about specific tools. It reads config, builds agents, connects MCP servers, and runs the loop.

## Config

```yaml
# config/config.yaml

slack:
  app_token: "${SLACK_APP_TOKEN}"
  bot_token: "${SLACK_BOT_TOKEN}"

mcp_servers:
  - name: grafana
    url: "https://grafana-mcp.internal.example.com/sse"
  - name: gcp-logging
    url: "https://gcp-logging-mcp.internal.example.com/sse"

coordinator:
  model: gemini-2.0-flash
  description: "Understands operator requests, picks relevant playbooks, delegates to specialists, aggregates results"
  instruction: "instructions/coordinator.md"
  temperature: 0.1

agents:
  - name: metrics-analyst
    model: gemini-2.5-pro
    description: "Queries dashboards and interprets metrics data"
    instruction: "instructions/metrics-analyst.md"
    temperature: 0.2
    tools: [grafana]

  - name: log-analyst
    model: gemini-2.5-pro
    description: "Searches and analyzes application logs"
    instruction: "instructions/log-analyst.md"
    temperature: 0.2
    tools: [gcp-logging]

playbooks_dir: "playbooks/"
```

MCP servers are deployed and managed separately. The bot connects to them via SSE as a client. ADK's `McpToolset` handles connection, tool discovery, and execution. Which agents use which MCP servers is defined in the agent config — the bot wires it up at startup. The `get_playbook` tool is built into the engine and provided automatically to the coordinator.

## Slack UX

### Focused investigation

```
Operator: @incidently problems with apple pay

Bot:      ⏳ Investigating...
Bot:      📊 Checking payment service metrics...
Bot:      📋 Searching logs for apple pay errors...

Bot:      🔍 Apple Pay Investigation — 14:31 UTC

          Payment Service
          🔴 Apple Pay success rate: 73% (baseline 99.2%)
          ⚠️ Latency p99: 4.2s (baseline 1.1s)
          ✅ Other payment methods: normal

          Logs
          ❌ "Apple Pay token validation timeout" — 312 occurrences since 14:15 UTC
          ❌ Affected endpoint: /api/payments/apple-pay/validate
          📈 Started at 14:15, coincides with Apple Pay gateway response time spike

          Likely cause: Apple Pay gateway is responding slowly,
          causing token validation timeouts.

          Suggest: check Apple's status page, monitor if it recovers.
```

### Broad health check

```
Operator: @incidently how's the system doing?

Bot:      ⏳ Analyzing...
Bot:      📊 Checking application metrics...
Bot:      📋 Reviewing logs...
Bot:      📊 Checking infrastructure...

Bot:      🏥 System Overview — 14:31 UTC

          Application
          ✅ Error rate: 0.8%
          ✅ Latency p99: 1.1s
          ✅ Throughput: 312 rps

          ✅ All clear. No anomalies detected.
```

### Follow-up in thread

```
Operator: what about compared to yesterday?

Bot:      ⏳ Comparing...

Bot:      📊 Yesterday vs Today (same hour)

          Throughput: 312 rps → 298 rps yesterday (normal variance)
          Error rate: 0.8% → 0.6% yesterday (slightly higher, within range)
          Latency: 1.1s → 1.0s yesterday (normal)

          No significant differences. System is behaving consistently.
```

### UX Principles

- **No commands** — just natural language
- Bot always replies **in thread**
- **@bot mentions** — explicit invocation, no accidental triggers
- **Progress messages** — intermediate updates during analysis
- **Suggests next steps** after every response when issues are found

## MVP Scope

### Included

- Natural language interaction via Slack (`@bot` mentions)
- Multi-agent architecture (coordinator + configurable specialists)
- Declarative agent config (model, temperature, tools, instructions per agent)
- Playbooks as knowledge base with YAML frontmatter
- Threaded conversation with session context (ADK in-memory sessions)
- Progress messages during analysis
- Read-only — analysis only, no automated actions

### Not in MVP

- Automatic alert triggers (add later — one webhook)
- Automated actions (restarts, scaling, failover)
- Session persistence (in-memory, lost on restart)
- Multi-user / access control
- Post-mortem generation
- Playbook hot-reload (requires restart)

## Post-MVP Roadmap

1. **Alert triggers** — alert webhook → bot auto-starts analysis
2. **Post-mortem draft** — bot generates summary from thread after incident closure
3. **Correlations** — temporal, deploy, cross-service
4. **Incident history** — search similar past incidents
5. **Persistent sessions** — Redis via ADK's session/database package
6. **Multi-user** — full on-call team interaction
7. **Playbook hot-reload** — detect changes without restart