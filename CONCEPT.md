# Incidently

AI-powered Slack bot for system health checks and incident analysis. Connects to your observability stack via MCP servers.

## What It Does

A Slack bot that runs a multi-agent system (powered by Gemini via Google ADK Go) to analyze system health through your existing observability tools. Connects to any tool that exposes an MCP server — Grafana, Datadog, GCP Logging, Elasticsearch, PagerDuty, or anything else. Operates in human-in-the-loop mode — the operator triggers checks and guides further analysis through conversation.

Three layers of configuration — no code changes needed to adapt to a different stack:

- **Playbooks** define WHAT to analyze (workflows, steps, concrete queries)
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
Coordinator Agent (fast model, no tools)
    ├── reads playbooks, breaks into steps
    ├── delegates steps to specialist agents
    ├── aggregates results into final summary
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

**Coordinator Agent** — the orchestrator. Uses a fast/cheap model. Has no MCP tools itself. Reads playbooks, breaks them into steps, decides which specialist to delegate each step to based on what the step requires and which agents are available. Aggregates results into the final summary.

**Specialist Agents** — defined entirely in config. Each has its own model, temperature, behavioral instructions, and scoped set of MCP tools. They receive tasks from the coordinator, execute them using their tools, and return structured findings. They don't know about playbooks — they just do what the coordinator asks.

**MCP Toolset** — ADK's built-in `McpToolset` connects to external MCP servers via SSE, discovers available tools, and makes them callable by agents. Each specialist is configured with only the MCP servers relevant to its role.

**Playbooks** — markdown files describing analysis workflows. The coordinator reads and executes them step by step.

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
playbooks/                — analysis workflows (WHAT to do)
config/
  config.yaml             — slack, MCP servers, agent definitions
```

## Multi-Agent Design

### Three Layers

**Playbooks** = WHAT to do. Domain-specific analysis workflows with concrete steps referencing dashboard names, log queries, and thresholds. Portable — change these when your system changes.

**Agent instructions** = HOW to behave. General behavioral guidelines per agent role — analysis approach, response format, error handling. Reusable across any playbook.

**Agent config** = infrastructure. Model, temperature, which MCP tools. Lives in `config.yaml`.

### How It Works

1. Operator sends `@bot health`
2. Slack gateway parses the message, sends progress indicator to the thread
3. ADK runner finds (or creates) the session for this thread
4. Coordinator agent receives the message
5. Coordinator reads available playbooks (loaded at startup with descriptions), matches request to the right playbook
6. Coordinator reads each step, decides which specialist agent should handle it based on what the step requires and what agents are available
7. Specialist agents execute their tasks using their MCP tools, return findings
8. Coordinator aggregates all findings following the playbook's output format
9. Final summary posted to Slack thread
10. Follow-up messages in the thread have full conversation context

### Agent Definitions

Agents are defined in `config.yaml` — fully declarative. The Go code reads this config and builds the agent tree. No agent roles are hardcoded.

```yaml
agents:
  - name: coordinator
    model: gemini-2.0-flash
    description: "Routes operator requests to specialists, aggregates results"
    instruction: "instructions/coordinator.md"
    temperature: 0.1
    tools: []

  # Everything below is an example — define whatever agents your stack needs

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

### Playbook Format

Playbooks have YAML frontmatter (for description) and markdown body (the workflow). They reference concrete identifiers (dashboard names, log queries, panel names) but are not tied to any specific tool — the coordinator figures out which agent can handle each step.

```yaml
---
description: "Full system health check — overview metrics, log errors, infrastructure"
---
```

```markdown
# Health Check

Perform a full system health check. Follow all steps in order.

## Step 1: Application Metrics

Query the "Main Overview" dashboard. Pull these panels for the last 15 minutes:
- Error Rate — separate system errors (5xx) from client errors (4xx)
- Latency p99
- Throughput

Compare each metric to the same time window last week.
Flag as warning if deviation > 20%, critical if > 50%.

## Step 2: Log Errors

Search application logs for the last 15 minutes, severity >= ERROR.
Identify top error messages, new error types, and affected services.

## Step 3: Infrastructure

Query the "Infrastructure" dashboard. Check:
- Pod Restarts — last 1 hour
- Queue Lag — last 15 minutes
- DB Connections — last 15 minutes

## Output format

Present results grouped by section. Use status indicators.
Only show details for warnings and critical items.
Suggest what to check next if there are issues.
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

**Anomaly ≠ Incident.** Playbooks instruct what to check; agent instructions tell the agent how to distinguish real problems from expected noise.

**Dynamic baseline.** No hardcoded thresholds in agent instructions. Compare to same weekday last week.

**Concrete identifiers.** Playbooks contain exact dashboard names, panel names, log queries, and filter values. The agent does not guess.

**Scoped tools.** Each specialist agent only sees the MCP tools relevant to its role. Less noise for the LLM, fewer wrong tool calls.

**Generic engine.** The Go code knows nothing about Grafana, logging, or any specific tool. It reads config, builds agents, connects MCP servers, and runs the loop.

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

agents:
  - name: coordinator
    model: gemini-2.0-flash
    description: "Routes operator requests to specialists, aggregates results"
    instruction: "instructions/coordinator.md"
    temperature: 0.1
    tools: []

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

MCP servers are deployed and managed separately. The bot connects to them via SSE as a client. ADK's `McpToolset` handles connection, tool discovery, and execution. Which agents use which MCP servers is defined in the agent config — the bot wires it up at startup.

## Slack UX

### Trigger — @bot mention

```
Operator: @incidently health

Bot:      ⏳ Analyzing...
Bot:      📊 Checking application metrics...
Bot:      📋 Searching logs...
Bot:      📊 Checking infrastructure...

Bot:      🏥 System Health Check — 14:31 UTC

          Application
          ✅ Error rate: 0.8%
          ⚠️ Latency p99: 1.1s (+22% vs last week)
          ✅ Throughput: 312 rps

          Dependencies
          ⚠️ ServiceZ: 94.2% — elevated timeouts
          ✅ Others: normal

          Logs
          ⚠️ 142 errors (baseline ~80)
          Top: "connection timeout to ServiceZ"

          Infrastructure
          ✅ All clear

          ⚠️ 2 warnings — both related to ServiceZ.
          Suggest: dig into ServiceZ for root cause.
```

### Conversation — follow-up in thread

```
Operator: dig into ServiceZ

Bot:      ⏳ Analyzing ServiceZ...

Bot:      ServiceZ — last hour:
          📉 Success rate: 94.2% → trend stable
          ❌ Top error: read timeout 10s — 84 occurrences
          🔀 Affected consumers: ConsumerA, ConsumerB

          Not critical, but worth monitoring.
```

### UX Principles

- Bot always replies **in thread**
- **@bot mentions** — explicit invocation, no accidental triggers
- **Progress messages** — intermediate updates during analysis
- **Natural language** — "check providers" works, no rigid command syntax
- **Suggests next steps** after every response

## MVP Scope

### Included

- Manual trigger via Slack (`@bot` mentions, natural language)
- Multi-agent architecture (coordinator + configurable specialists)
- Declarative agent config (model, temperature, tools, instructions per agent)
- Playbooks with YAML frontmatter
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