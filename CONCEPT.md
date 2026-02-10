# Incidently

AI-powered Slack bot for system health checks and incident analysis. Connects to your observability stack via MCP servers.

## What It Does

A Slack bot that connects to your observability tools (Grafana, Datadog, GCP Logging, Elasticsearch, etc.) via MCP servers, analyzes system health using an LLM (Gemini), and delivers structured summaries in a Slack thread. Operates in human-in-the-loop mode — the operator triggers checks and guides further analysis through conversation. All analysis logic lives in portable markdown playbooks — no code changes needed to adapt to a different stack.

## Stack

- **Go** — primary language
- **Google ADK Go** (`google.golang.org/adk`) — agent framework with built-in MCP toolset, ReAct loop, and LLM abstraction
- **slack-go/slack** — Slack bot (Socket Mode)
- **In-memory (sync.Map)** — session state (Redis later)

## Architecture

```
Slack (operator)
    ↓
Slack Gateway (socket mode, threads)
    ↓
Session Manager (per-thread conversation state)
    ↓
ADK Agent (Google ADK Go)
    ├── LLM (Gemini or other provider via ADK)
    ├── MCP Toolset (auto-discovers tools from configured MCP servers)
    └── Playbooks (markdown instructions injected as agent instructions)
    ↓
Slack Gateway (reply in thread)
```

### Components

**Slack Gateway** — receives messages via Socket Mode. Distinguishes new commands from ongoing thread conversations. Always replies in thread.

**Session Manager** — each Slack thread = separate session. Stores message history for the ADK agent context. MVP uses sync.Map.

**ADK Agent** — core of the system. Google ADK Go handles the ReAct loop (LLM reasoning → tool calls → observation → response), MCP tool discovery, and LLM communication. The agent's instructions are loaded from markdown playbooks.

**MCP Toolset** — ADK's built-in `McpToolset` connects to external MCP servers, discovers available tools, and makes them callable by the agent. No custom MCP client code needed.

**Playbooks** — markdown files loaded as agent instructions (details below).

## Package Structure

```
/cmd/bot              — entrypoint
/internal/
  /slack              — gateway, message parsing, thread management
  /session            — session manager, conversation state
  /agent              — ADK agent setup, playbook loader, MCP toolset config
  /config             — config loader

/playbooks/           — markdown instructions for the agent (portable across installations)
  health-check.md     — example: full system health check
  drill-down.md       — example: deep dive into a specific component

/config/
  config.yaml         — Slack, LLM, and MCP server connection settings
```

## Playbooks

Playbooks are **markdown files** — natural language instructions loaded as the ADK agent's system instructions. They describe what to check, in what order, and how to interpret results. They reference concrete dashboard UIDs, panel names, and log queries so the agent knows exactly which MCP tool calls to make.

The Go application sets up an ADK agent with: playbook text as instructions, MCP toolsets pointing to configured servers, and the LLM provider. ADK handles the ReAct loop — the agent reads its instructions, decides which tools to call, observes results, and formulates a response. All domain logic lives in the playbooks.

This makes the project **portable**: to adapt Incidently for a different system, write new playbooks and update MCP server config — no Go code changes needed.

### How It Works

1. Operator triggers `/health`
2. Bot loads `playbooks/health-check.md` as agent instructions
3. ADK agent is initialized with the playbook instructions + MCP toolsets (connected to servers from config)
4. Operator's message is passed to the agent
5. ADK runs the ReAct loop: agent reads playbook → calls MCP tools (dashboards, logs) → observes results → reasons about next step → repeats until done
6. Agent's final response is posted to Slack thread
7. On follow-up messages in the thread, the agent has full conversation history and can load drill-down playbooks as needed

### Playbook Routing

The agent needs to know which playbook to use. MVP approach: **single root playbook + slash-command shortcuts**.

One main playbook is always loaded as the agent's instructions. It contains all analysis scenarios and describes when to use each one. The agent's ReAct loop decides which section to execute based on the operator's message.

Slash commands act as shortcuts that load specific playbooks with a pre-filled intent:

- `/health` → loads `health-check.md` + sends "perform a health check"
- `/playbook infra` → loads a specific playbook by name
- Free-text message in thread → agent already has the playbook + session context, figures out what to do

The root playbook contains routing logic as part of its instructions:

```markdown
## When the operator asks for a health check
Perform the full overview (Steps 1-3 below).

## When the operator asks to dig into a specific component
Skip the overview. Go directly to detailed analysis
of that component (Step 4).

## When the operator asks a follow-up question
Use the data already collected in this session to answer.
If you need more data, query it.
```

This keeps things simple — one playbook, one agent, no router layer. When the number of playbooks grows and they become too large for a single context, introduce a lightweight router agent that picks the right playbook based on the operator's message and available playbook descriptions.

### Playbook Format

Playbooks are written as agent instructions — the same way you'd explain the analysis process to a new engineer, but with exact technical identifiers needed for automation.

Example (`playbooks/health-check.md`):

```markdown
# Health Check

You are performing a system health check. Use the available MCP tools
to query dashboards and search logs.

## Step 1: Overview

Query dashboard UID `main-overview`. Pull these panels for the last
15 minutes:

- **Error Rate** — separate system errors (5xx, timeouts) from client
  errors (4xx). If only client errors spiked, the system is healthy —
  note the source but don't flag it.
- **Latency p99**
- **Throughput**

Compare each metric to the same time last week. Flag as warning if
deviation > 20%, critical if > 50%. If throughput is lower but it's
a weekend or night — that's likely normal, note it but don't flag.

## Step 2: Dependencies

Query dashboard UID `dependencies-overview`. This shows all external
dependencies with their current success rate and response time.

Identify dependencies where success rate deviates more than 20% from
their average over the past week.

For each degraded dependency:
1. Drill into its dedicated dashboard (linked from the overview)
2. Check which error codes are increasing
3. Search logs for that dependency name — look for maintenance
   notices or upstream errors
4. Identify which consumers are affected

## Step 3: Infrastructure

Query dashboard UID `infra-001`:
- **Pod Restarts** panel — last 1 hour
- **Queue Lag** panel — last 15 minutes
- **DB Connections** panel — last 15 minutes

If pods restarted, check if there was a deployment in the last hour.

## Output

Present results grouped by: Application, Dependencies, Infrastructure.
Use ✅ ⚠️ 🔴 indicators. Only show details for anomalies.
Always suggest what to check next if there are warnings or critical items.
Include dashboard links where relevant.
```

### Drill-Down Playbooks

When the operator asks to dig deeper, the agent loads the appropriate drill-down playbook. These are domain-specific — each installation writes its own based on its architecture. Examples:

- Detailed dependency analysis: error breakdown, timeline, affected consumers, log search
- Infrastructure deep dive: pod restarts with reasons, queue consumer details, DB slow queries, deployment correlation
- Service-specific analysis: per-service metrics, error patterns, upstream/downstream impact

Each drill-down playbook contains its own dashboard UIDs, log queries, and analysis logic.

### Key Principles

**Anomaly ≠ Incident.** Playbooks should instruct the agent to distinguish between real system problems and expected noise (e.g. client-side errors, normal traffic variance, scheduled maintenance).

**Dynamic baseline.** No hardcoded thresholds. Compare to same weekday last week. Minimum sample size to avoid false positives on low traffic.

**Concrete identifiers.** Every playbook contains exact dashboard UIDs, panel names, log queries, and filter values. The agent must not guess — it reads the playbook and knows exactly what MCP calls to make.

## Config

Only minimal static configuration that the engine needs to operate:

```yaml
# config/config.yaml

slack:
  app_token: "${SLACK_APP_TOKEN}"
  bot_token: "${SLACK_BOT_TOKEN}"

llm:
  provider: "gemini"
  model: "gemini-2.5-pro"
  api_key: "${GEMINI_API_KEY}"

mcp_servers:
  - name: grafana
    url: "https://grafana-mcp.internal.example.com/sse"

  - name: gcp-logging
    url: "https://gcp-logging-mcp.internal.example.com/sse"
```

MCP servers are deployed and managed separately. The bot connects to them via SSE as a client. ADK's `McpToolset` handles connection, tool discovery, and execution.

## Slack UX

### Trigger — Manual

```
Operator: /health

Bot: 🔍 Checking system health...

Bot: 🏥 System Health Check — 14:31 UTC

     Application
     ✅ Error rate: 0.8%
     ✅ Latency p99: 1.1s
     ✅ Throughput: 312 rps (last week: 305 rps)

     Dependencies
     ✅ ServiceX: 99.1%
     ⚠️ ServiceZ: 94.2% — elevated timeouts
     ✅ Others: normal

     Infrastructure
     ✅ CPU/Memory: normal
     ✅ Queue lag: 12ms
     ✅ DB connections: 45/200

     ⚠️ 1 warning: ServiceZ

     Dig into ServiceZ?
```

### Conversation — Natural Language in Thread

```
Operator: let's see

Bot: ServiceZ last hour:
     📉 Success rate: 94.2% → trend stable
     ❌ Top error: read timeout 10s — 84 times
     🔀 Affected: ConsumerA, ConsumerB
     📊 Volume: 4.2% of total traffic
     Not critical, worth monitoring.
     📎 [ServiceZ dashboard link]
```

### Launch Variations

- `/health` — full health check
- `check dependencies` — specific area
- `what's up with ServiceX?` — specific component
- `how are we compared to yesterday?` — comparison

### UX Principles

- Bot always replies **in thread**
- **Natural language** — "check providers" works same as slash commands
- **Dashboard links** — bot complements your observability tools, doesn't replace them
- **Suggests next steps** after every response
- **Timers** — "remind me to check in 20 minutes"

## MVP Scope

### Included

- Manual trigger via Slack (`/health`, natural language)
- Health Check playbook with drill-down support
- Threaded conversation with session context
- Dynamic baseline (weekly comparison)
- Read-only — analysis only, no automated actions

### Not in MVP

- Automatic alert triggers (add later — one webhook)
- Automated actions (restarts, scaling, failover)
- Session persistence (in-memory, lost on restart)
- Multi-user / access control
- Post-mortem generation

## Post-MVP Roadmap

1. **Alert triggers** — alert webhook → bot auto-starts analysis
2. **Post-mortem draft** — bot generates summary from thread after incident closure
3. **Correlations** — temporal, deploy, cross-service
4. **Incident history** — search similar past incidents
5. **Redis** — persistent sessions
6. **Multi-user** — full on-call team interaction