# Implementation Plan

Step-by-step plan for building Incidently MVP. Each step produces a compilable increment. Each step includes an explanation of what was built and why.

## Decisions made before implementation

- **Triggering**: `@bot` mentions in Slack channels, not slash commands. Simpler setup, no 3-second ACK deadline.
- **Multi-agent**: Coordinator agent reads playbooks and delegates steps to specialist agents. Specialists have scoped MCP tools and behavioral instructions. All defined in config.
- **Playbooks vs instructions**: Playbooks = WHAT to do (workflow steps). Agent instructions = HOW to behave (personality, analysis approach). Separate concerns.
- **Generic engine**: Go code knows nothing about specific tools. It reads config, builds agents, connects MCP servers, runs the loop. All domain knowledge lives in config, playbooks, and instructions.
- **Progress messages**: Bot sends intermediate updates to Slack while the agent works so the user knows something is happening.
- **Error handling (MVP)**: If a tool call fails mid-analysis, the agent reports what it has and notes the failure. Instruction baked into coordinator's prompt.
- **Concurrency**: Single user assumed. No connection pooling, no locks beyond what ADK provides.
- **Sessions**: ADK's `session.InMemoryService()`. Each Slack thread = one session. Lost on restart, fine for MVP.
- **MCP servers**: Already running on test env. Bot connects via SSE. ADK handles tool discovery.
- **Testing**: Against real infrastructure, no mocks.

## Package structure

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

Three internal packages. Session management lives inside `agent` — just a call to ADK's `session.InMemoryService()`.

---

## Step 1: Project skeleton and config

Create the Go module, directories, and config loading.

**What to do:**
- `go mod init`
- Create the directory layout
- Define `config/config.yaml` with the full schema:
  - `slack`: app_token, bot_token
  - `mcp_servers`: list of {name, url}
  - `agents`: list of {name, model, description, instruction, temperature, tools}
  - `playbooks_dir`: path to playbooks directory
- Write `internal/config/config.go`:
  - Structs matching the yaml schema (Config, SlackConfig, MCPServerConfig, AgentConfig)
  - `Load(path string) (*Config, error)` — reads yaml, resolves `${ENV_VAR}` references from environment
  - Validation: check required fields, verify instruction files exist, verify tools reference defined MCP servers
- Write `cmd/bot/main.go` — loads config, logs it, exits

**Explanation:** Config is the foundation and now carries more weight — it defines the entire agent topology, not just connection strings. The agent definitions (model, temperature, tools, instruction path) all live here. Validation at load time catches misconfigurations early (e.g., an agent referencing an MCP server that isn't defined). After this step we can run the binary and confirm config loads correctly.

---

## Step 2: Slack gateway — connect and listen

Connect to Slack via Socket Mode and react to @bot mentions.

**What to do:**
- Add `github.com/slack-go/slack` and `github.com/slack-go/slack/socketmode` dependencies
- Write `internal/slack/gateway.go`:
  - `Gateway` struct with Slack client and socket mode client
  - `New(cfg config.SlackConfig) (*Gateway, error)`
  - `Run(ctx context.Context, handler func(msg Message))` — connects via Socket Mode, listens for events
  - Listen for `EventsAPI` envelope → `AppMentionEvent`
  - Parse each event into a `Message` struct: channel, thread timestamp (use message ts if not in a thread), user ID, text (with bot mention prefix stripped)
  - `PostMessage(channel, threadTS, text string) error` — sends a reply in the thread
  - For now, the handler echoes back the parsed text (proves the round-trip works)
- Wire in `main.go`: load config → create gateway → run

**Explanation:** Socket Mode means no public URL, no webhook server — the bot dials out to Slack over a WebSocket. We listen for `AppMentionEvent` specifically, so the bot only responds when explicitly addressed with `@bot`. Thread timestamp is the key identifier — every reply goes to a thread, and this same timestamp becomes the ADK session ID later. The echo handler validates the full Slack round-trip before we add any AI logic.

---

## Step 3: Playbook and instruction loader

Load playbook markdown files and agent instruction files.

**What to do:**
- Write `internal/agent/playbook.go`:
  - `Playbook` struct: `Name string`, `Description string`, `Content string`
  - `LoadPlaybooks(dir string) ([]Playbook, error)` — reads all `.md` files from the directory, parses YAML frontmatter to extract `description`, rest is `Content`. `Name` from filename without extension
  - `LoadInstruction(path string) (string, error)` — reads a markdown file and returns its content as a string (used for agent instructions)
  - `BuildPlaybookIndex(playbooks []Playbook) string` — builds a summary listing all playbook names + descriptions, for injection into the coordinator's instructions
- Create sample `playbooks/health-check.md` with frontmatter and placeholder steps
- Create sample `instructions/coordinator.md` with placeholder coordinator behavior

**Explanation:** Two types of markdown files serve different purposes. Playbooks describe workflows (WHAT to do) — loaded with frontmatter parsing so we can build an index for the coordinator. Agent instructions describe behavior (HOW to act) — loaded as plain markdown strings and injected as system instructions into each agent. The playbook index gets appended to the coordinator's instructions so it knows what playbooks are available and can match operator requests to the right one. Specialist agents never see playbooks — they only get their own behavioral instructions.

---

## Step 4: Multi-agent setup with MCP toolsets

Build the agent tree from config: coordinator + specialists, each with their own model, instructions, and MCP tools.

**What to do:**
- Add `google.golang.org/adk` and MCP Go SDK dependencies
- Write `internal/agent/agent.go`:
  - `Service` struct holding: ADK `runner.Runner`, `session.Service`, list of MCP toolsets (for cleanup)
  - `NewService(cfg *config.Config, playbooks []Playbook) (*Service, error)`:
    1. Create MCP toolsets: for each MCP server in config, create `mcptoolset.New()` with SSE transport. Store in a map: server name → toolset
    2. Load agent instructions: for each agent in config, call `LoadInstruction(agent.Instruction)`
    3. Build specialist agents: for each non-coordinator agent, create `llmagent.New()` with its model, temperature, loaded instruction, and only the MCP toolsets matching its `tools` list
    4. Build coordinator agent: create `llmagent.New()` with its model, temperature, its own instruction + playbook index appended, and the specialist agents registered as sub-agents (via ADK's agent transfer mechanism)
    5. Create `session.InMemoryService()`
    6. Create `runner.New()` with the coordinator as root agent and the session service
  - `HandleMessage(ctx context.Context, userID string, threadTS string, text string, onProgress func(string)) (string, error)`:
    - Get or create session by threadTS
    - Build `*genai.Content` from user text
    - Call `runner.Run(ctx, userID, threadTS, msg, runCfg)`
    - Iterate over events:
      - On tool call events or agent transfer events → call `onProgress`
      - On final response → collect the text
    - Return the final response text
  - `Close()` — closes all MCP toolset connections

**Explanation:** This is where config becomes a running system. The Go code reads agent definitions and builds an ADK agent tree — coordinator at the root, specialists as sub-agents. Each specialist gets only the MCP tools from its config (e.g., if an agent lists `tools: [grafana]`, it only gets the Grafana MCP toolset). The coordinator gets no MCP tools but knows about all specialists via ADK's agent transfer/delegation. When the coordinator delegates a step to a specialist, ADK handles the hand-off — the specialist runs its own ReAct loop with its own tools and returns results. The coordinator's instructions include the playbook index so it can match requests to playbooks, plus all playbook content so it can execute them step by step.

---

## Step 5: Connect the full loop with progress messages

Wire Slack gateway → agent service → Slack reply, with intermediate progress updates.

**What to do:**
- Update `internal/slack/gateway.go`:
  - Replace the echo handler with a real handler that:
    1. Sends an initial "Analyzing..." message to the thread
    2. Calls `agent.Service.HandleMessage()`, passing a progress callback that posts to the thread
    3. Posts the agent's final response to the thread
    4. On error, posts an error message to the thread
  - Throttle progress messages — at most one every 5 seconds to avoid spamming the thread
- Update `main.go` — full wiring: config → playbooks → agent service → slack gateway with handler
- Add graceful shutdown: listen for SIGINT/SIGTERM, cancel context, close agent service (MCP connections), disconnect Slack

**Explanation:** This connects all the pieces into a working system. A Slack message arrives → gateway parses it → agent service creates/retrieves the ADK session → coordinator agent reads the playbook, delegates steps to specialists → specialists call MCP tools and return findings → coordinator aggregates and produces the summary → response posted to Slack thread. Progress callbacks fire on agent transfers and tool calls so the user sees intermediate updates. Graceful shutdown ensures MCP connections close cleanly and Slack disconnects properly.

---

## Step 6: Sample instructions, playbook, and end-to-end test

Create real agent instructions and a playbook targeting the test environment. Test the full flow.

**What to do:**
- Write real agent instruction files in `instructions/`:
  - `coordinator.md` — how to read playbooks, delegate, aggregate, handle failures
  - Specialist instructions matching the agents defined in config — behavioral guidelines and response format for each
- Write real `playbooks/health-check.md` targeting the test environment:
  - Steps referencing actual dashboard names and log queries from the test MCP servers
  - Output format instructions
  - Drill-down guidance
- Run the bot locally:
  - `@bot health` in Slack → verify: progress messages appear → final summary in thread
  - Send a follow-up in the thread → verify session context works
  - Test natural language: "check dependencies", "what's up with ServiceX?"
  - Verify that the coordinator correctly delegates to the right specialists
- Fix issues discovered during testing

**Explanation:** Everything built in steps 1-5 is generic infrastructure. This step adds the domain content — real instructions that make agents useful and a real playbook that targets actual dashboards and logs. This is the integration test against real infrastructure. It validates the full chain: Slack → gateway → session → coordinator → specialist → MCP tools → LLM reasoning → aggregation → Slack thread. Since agent roles and tools are defined in config, this step also proves the config-driven approach works — changing what the bot can do requires only editing yaml, markdown, and instruction files.

---

## Dependency graph

```
Step 1 (skeleton + config)
  ├── Step 2 (slack gateway)
  └── Step 3 (playbook + instruction loader)
        │
Step 4 (multi-agent + MCP)  ← depends on 1, 3
        │
Step 5 (full loop)  ← depends on 2, 4
        │
Step 6 (real content + e2e test)
```

Steps 2 and 3 can be built in parallel. Step 4 needs 1 and 3. Step 5 integrates everything. Step 6 is validation.