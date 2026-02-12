package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/illenko/incidently/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/mcptoolset"
	"google.golang.org/genai"
)

type Service struct {
	runner   *runner.Runner
	sessions session.Service
	toolsets []tool.Toolset
}

type GetPlaybookArgs struct {
	Name string `json:"name" jsonschema:"description=Name of the playbook to load"`
}

type GetPlaybookResult struct {
	Content string `json:"content"`
}

func NewService(ctx context.Context, cfg *config.Config, playbooks []Playbook) (*Service, error) {
	slog.Info("initializing agent service")

	mcpToolsets := make(map[string]tool.Toolset)
	var allToolsets []tool.Toolset

	for _, srv := range cfg.MCPServers {
		slog.Info("creating MCP toolset", "name", srv.Name, "url", srv.URL)
		transport := &mcp.SSEClientTransport{Endpoint: srv.URL}
		ts, err := mcptoolset.New(mcptoolset.Config{Transport: transport})
		if err != nil {
			return nil, fmt.Errorf("creating MCP toolset %s: %w", srv.Name, err)
		}
		mcpToolsets[srv.Name] = ts
		allToolsets = append(allToolsets, ts)
		slog.Info("MCP toolset created", "name", srv.Name)
	}

	playbookMap := make(map[string]string)
	for _, pb := range playbooks {
		playbookMap[pb.Name] = pb.Content
	}
	slog.Info("registered playbooks for get_playbook tool", "count", len(playbookMap))

	getPlaybookTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_playbook",
			Description: "Loads the full content of a playbook by name. Use this to get detailed investigation steps.",
		},
		func(ctx tool.Context, args GetPlaybookArgs) (GetPlaybookResult, error) {
			content, ok := playbookMap[args.Name]
			if !ok {
				slog.Warn("playbook not found", "name", args.Name)
				return GetPlaybookResult{}, fmt.Errorf("playbook %q not found", args.Name)
			}
			slog.Info("playbook loaded by coordinator", "name", args.Name, "size_bytes", len(content))
			return GetPlaybookResult{Content: content}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("creating get_playbook tool: %w", err)
	}

	var subAgents []agent.Agent
	for _, agentCfg := range cfg.Agents {
		slog.Info("building specialist agent", "name", agentCfg.Name, "model", agentCfg.Model, "tools", agentCfg.Tools)
		a, err := buildAgent(ctx, agentCfg, mcpToolsets)
		if err != nil {
			return nil, fmt.Errorf("building agent %s: %w", agentCfg.Name, err)
		}
		subAgents = append(subAgents, a)
		slog.Info("specialist agent ready", "name", agentCfg.Name)
	}

	slog.Info("building coordinator", "model", cfg.Coordinator.Model, "sub_agents", len(subAgents))
	coordinator, err := buildCoordinator(ctx, cfg.Coordinator, playbooks, getPlaybookTool, subAgents)
	if err != nil {
		return nil, fmt.Errorf("building coordinator: %w", err)
	}
	slog.Info("coordinator ready")

	sessionService := session.InMemoryService()

	r, err := runner.New(runner.Config{
		AppName:        "incidently",
		Agent:          coordinator,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("creating runner: %w", err)
	}

	slog.Info("agent service initialized")
	return &Service{
		runner:   r,
		sessions: sessionService,
		toolsets: allToolsets,
	}, nil
}

func (s *Service) HandleMessage(
	ctx context.Context,
	userID, threadTS, text string,
	onProgress func(string),
) (string, error) {
	slog.Info("handling message", "user", userID, "thread", threadTS, "text", text)

	msg := genai.NewContentFromText(text, genai.RoleUser)

	var parts []string

	for event, err := range s.runner.Run(ctx, userID, threadTS, msg, agent.RunConfig{}) {
		if err != nil {
			slog.Error("runner event error", "error", err, "thread", threadTS)
			return "", fmt.Errorf("agent error: %w", err)
		}

		if event.Actions.TransferToAgent != "" {
			slog.Info("agent transfer",
				"from", event.Author,
				"to", event.Actions.TransferToAgent,
				"thread", threadTS,
			)
			onProgress(fmt.Sprintf("Delegating to %s...", event.Actions.TransferToAgent))
		}

		if event.Content != nil {
			for _, part := range event.Content.Parts {
				if part.FunctionCall != nil {
					slog.Debug("tool call",
						"agent", event.Author,
						"tool", part.FunctionCall.Name,
						"thread", threadTS,
					)
				}
			}
		}

		if event.IsFinalResponse() {
			slog.Debug("final response from agent", "agent", event.Author, "thread", threadTS)
			if event.Content != nil {
				for _, part := range event.Content.Parts {
					if part.Text != "" {
						parts = append(parts, part.Text)
					}
				}
			}
		}
	}

	result := strings.Join(parts, "\n")
	slog.Info("message handled", "thread", threadTS, "response_length", len(result))
	return result, nil
}

func (s *Service) Close() {
	slog.Info("closing agent service")
	for _, ts := range s.toolsets {
		if closer, ok := ts.(interface{ Close() }); ok {
			closer.Close()
		}
	}
	slog.Info("agent service closed")
}

func buildAgent(ctx context.Context, cfg config.AgentConfig, mcpToolsets map[string]tool.Toolset) (agent.Agent, error) {
	m, err := gemini.NewModel(ctx, cfg.Model, &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("creating model: %w", err)
	}

	instruction, err := LoadInstruction(cfg.Instruction)
	if err != nil {
		return nil, fmt.Errorf("loading instruction: %w", err)
	}
	slog.Debug("loaded instruction", "agent", cfg.Name, "path", cfg.Instruction, "size_bytes", len(instruction))

	var agentToolsets []tool.Toolset
	for _, toolName := range cfg.Tools {
		if ts, ok := mcpToolsets[toolName]; ok {
			agentToolsets = append(agentToolsets, ts)
		}
	}

	return llmagent.New(llmagent.Config{
		Name:        cfg.Name,
		Description: cfg.Description,
		Model:       m,
		Instruction: instruction,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature: genai.Ptr(float32(cfg.Temperature)),
		},
		Toolsets: agentToolsets,
	})
}

func buildCoordinator(
	ctx context.Context,
	cfg config.CoordinatorConfig,
	playbooks []Playbook,
	getPlaybookTool tool.Tool,
	subAgents []agent.Agent,
) (agent.Agent, error) {
	m, err := gemini.NewModel(ctx, cfg.Model, &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("creating coordinator model: %w", err)
	}

	instruction, err := LoadInstruction(cfg.Instruction)
	if err != nil {
		return nil, fmt.Errorf("loading coordinator instruction: %w", err)
	}
	slog.Debug("loaded coordinator instruction", "path", cfg.Instruction, "size_bytes", len(instruction))

	playbookIndex := BuildPlaybookIndex(playbooks)
	slog.Debug("built playbook index", "size_bytes", len(playbookIndex))

	return llmagent.New(llmagent.Config{
		Name:        "coordinator",
		Description: cfg.Description,
		Model:       m,
		Instruction: instruction + "\n\n" + playbookIndex,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature: genai.Ptr(float32(cfg.Temperature)),
		},
		SubAgents: subAgents,
		Tools:     []tool.Tool{getPlaybookTool},
	})
}
