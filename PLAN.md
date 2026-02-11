# Implementation Plan

Step-by-step plan for building Incidently MVP. Each step produces a compilable increment. Each step includes an explanation of what was built and why.

## Decisions made before implementation

- **No commands**: The bot has no rigid commands. Operator describes a problem or asks a question in natural language. The coordinator decides what to investigate.
- **Triggering**: `@bot` mentions in Slack channels. Socket Mode, no public URL needed.
- **Multi-agent**: Coordinator agent has playbooks as a knowledge base. It understands the request, picks relevant steps, and delegates to specialist agents. Specialists have scoped MCP tools and behavioral instructions. All defined in config.
- **Playbook selection — two-phase**: Coordinator sees only a playbook index (name + description + tags) in its instructions. It matches the request against the index and calls `get_playbook(name)` to load full content only for relevant playbooks. Scales to dozens of playbooks without bloating context.
- **Playbooks as knowledge base**: Playbooks are not rigid scripts to run end-to-end. The coordinator draws from them selectively — picking only the steps relevant to the operator's request. "Problems with Apple Pay" loads the payment playbook, not infrastructure.
- **Playbooks vs instructions**: Playbooks = domain knowledge (WHAT to check, concrete queries). Agent instructions = behavior (HOW to act, response format). Separate concerns.
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
playbooks/                — domain knowledge (WHAT to check)
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
  - `coordinator`: {model, instruction, temperature} — separate from agents, the engine gives it playbook index, `get_playbook` tool, and all agents as sub-agents automatically
  - `agents`: list of {name, model, description, instruction, temperature, tools} — specialist agents only
  - `playbooks_dir`: path to playbooks directory
- Write `internal/config/config.go`:
  - Structs matching the yaml schema (Config, SlackConfig, MCPServerConfig, CoordinatorConfig, AgentConfig)
  - `Load(path string) (*Config, error)` — reads yaml, resolves `${ENV_VAR}` references from environment
  - Validation: check required fields, verify instruction files exist, verify agent tools reference defined MCP servers
- Write `cmd/bot/main.go` — loads config, logs it, exits

**Explanation:** Config is the foundation and carries significant weight — it defines the entire agent topology, not just connection strings. The agent definitions (model, temperature, tools, instruction path) all live here. Validation at load time catches misconfigurations early (e.g., an agent referencing an MCP server that isn't defined). After this step we can run the binary and confirm config loads correctly.

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
  - `Playbook` struct: `Name string`, `Description string`, `Tags []string`, `Content string`
  - `LoadPlaybooks(dir string) ([]Playbook, error)` — reads all `.md` files from the directory, parses YAML frontmatter to extract `description` and `tags`, rest is `Content`. `Name` from filename without extension
  - `LoadInstruction(path string) (string, error)` — reads a markdown file and returns its content as a string (used for agent instructions)
  - `BuildPlaybookIndex(playbooks []Playbook) string` — builds a compact index listing each playbook's name, description, and tags. This is what goes into the coordinator's instructions — small enough to scale to 30+ playbooks (~50 tokens each)
  - `GetPlaybookByName(name string) (string, error)` — returns full content of a playbook by name. This backs the `get_playbook` custom tool
- Create sample `playbooks/health-check.md` with frontmatter (including tags) and placeholder steps
- Create sample `instructions/coordinator.md` with placeholder coordinator behavior

**Explanation:** Two types of markdown files serve different purposes. Playbooks are domain knowledge (WHAT to check) — loaded with frontmatter parsing so we can build an index for the coordinator. Agent instructions describe behavior (HOW to act) — loaded as plain markdown strings and injected as system instructions into each agent. Critically, the coordinator does NOT get full playbook content in its instructions — only the compact index (name + description + tags). To access full content, the coordinator calls the `get_playbook` tool. This two-phase approach keeps the coordinator's context lean even with dozens of playbooks. Tags in the frontmatter give the coordinator better matching signal without needing to load content (e.g., tags: [payments, apple-pay] helps match "problems with apple pay").

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
    3. Create `get_playbook` custom function tool: a Go function tool that takes a playbook name and returns its full content via `GetPlaybookByName()`. This is an ADK function tool, not an MCP tool
    4. Build specialist agents: for each agent in `config.Agents`, create `llmagent.New()` with its model, temperature, loaded instruction, and only the MCP toolsets matching its `tools` list
    5. Build coordinator: read `config.Coordinator`, create `llmagent.New()` with its model, temperature, its own instruction + playbook index appended, the `get_playbook` function tool, and all specialist agents registered as sub-agents (via ADK's agent transfer mechanism). The coordinator is separate from the agents list — the engine knows it's special
    6. Create `session.InMemoryService()`
    7. Create `runner.New()` with the coordinator as root agent and the session service
  - `HandleMessage(ctx context.Context, userID string, threadTS string, text string, onProgress func(string)) (string, error)`:
    - Get or create session by threadTS
    - Build `*genai.Content` from user text
    - Call `runner.Run(ctx, userID, threadTS, msg, runCfg)`
    - Iterate over events:
      - On tool call events or agent transfer events → call `onProgress`
      - On final response → collect the text
    - Return the final response text
  - `Close()` — closes all MCP toolset connections

**Explanation:** This is where config becomes a running system. The Go code reads agent definitions and builds an ADK agent tree — coordinator at the root, specialists as sub-agents. Each specialist gets only the MCP tools from its config. The coordinator gets no MCP tools but has: (1) the playbook index in its instructions for matching requests to playbooks, (2) the `get_playbook` function tool to load full content on demand, and (3) specialist agents via ADK's agent transfer/delegation. When the operator says "problems with apple pay", the coordinator reads the playbook index, sees that `payment-investigation` has tags matching the request, calls `get_playbook("payment-investigation")` to load it, picks relevant steps, and delegates to the right specialists. The context stays lean — only loaded playbooks enter it, not all 30.

---

## Step 5: Connect the full loop with progress messages

Wire Slack gateway → agent service → Slack reply, with intermediate progress updates.

**What to do:**
- Update `internal/slack/gateway.go`:
  - Replace the echo handler with a real handler that:
    1. Sends an initial progress message to the thread
    2. Calls `agent.Service.HandleMessage()`, passing a progress callback that posts to the thread
    3. Posts the agent's final response to the thread
    4. On error, posts an error message to the thread
  - Throttle progress messages — at most one every 5 seconds to avoid spamming the thread
- Update `main.go` — full wiring: config → playbooks → agent service → slack gateway with handler
- Add graceful shutdown: listen for SIGINT/SIGTERM, cancel context, close agent service (MCP connections), disconnect Slack

**Explanation:** This connects all the pieces into a working system. A Slack message arrives → gateway parses it → agent service creates/retrieves the ADK session → coordinator reads playbook index, calls `get_playbook` for relevant ones, picks steps, delegates to specialists → specialists call MCP tools and return findings → coordinator aggregates and produces a focused summary → response posted to Slack thread. Progress callbacks fire on agent transfers and tool calls so the user sees intermediate updates. Graceful shutdown ensures MCP connections close cleanly and Slack disconnects properly.

---

## Step 6: Sample instructions, playbooks, and end-to-end test

Create real agent instructions and playbooks targeting the test environment. Test the full flow.

**What to do:**
- Write real agent instruction files in `instructions/`:
  - `coordinator.md` — how to use playbook knowledge, delegate, aggregate, handle failures
  - Specialist instructions matching the agents defined in config — behavioral guidelines and response format for each
- Write real playbooks in `playbooks/` targeting the test environment:
  - Reference actual dashboard names and log queries from the test MCP servers
  - Include output format guidance
- Run the bot locally and test diverse scenarios:
  - Focused investigation: `@bot problems with X` → verify it picks relevant steps, not a full scan
  - Broad request: `@bot how's the system doing?` → verify it does a comprehensive check
  - Follow-up in thread: verify session context works, operator can drill deeper
  - Verify that the coordinator correctly delegates to the right specialists
  - Verify progress messages appear during analysis
- Fix issues discovered during testing

**Explanation:** Everything built in steps 1-5 is generic infrastructure. This step adds the domain content — real instructions that make agents useful and real playbooks targeting actual dashboards and logs. Key validations: (1) the two-phase playbook selection works — coordinator reads the index, loads only relevant playbooks via `get_playbook`, not all of them; (2) a focused request triggers only relevant steps while a broad request loads more playbooks; (3) tags in frontmatter help the coordinator match accurately. This proves the natural language approach works at scale — no commands, lean context, just the operator describing what they need.

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