package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Slack        SlackConfig       `yaml:"slack"`
	MCPServers   []MCPServerConfig `yaml:"mcp_servers"`
	Coordinator  CoordinatorConfig `yaml:"coordinator"`
	Agents       []AgentConfig     `yaml:"agents"`
	PlaybooksDir string            `yaml:"playbooks_dir"`
}

type SlackConfig struct {
	AppToken string `yaml:"app_token"`
	BotToken string `yaml:"bot_token"`
}

type MCPServerConfig struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

type CoordinatorConfig struct {
	Model       string  `yaml:"model"`
	Description string  `yaml:"description"`
	Instruction string  `yaml:"instruction"`
	Temperature float64 `yaml:"temperature"`
}

type AgentConfig struct {
	Name        string   `yaml:"name"`
	Model       string   `yaml:"model"`
	Description string   `yaml:"description"`
	Instruction string   `yaml:"instruction"`
	Temperature float64  `yaml:"temperature"`
	Tools       []string `yaml:"tools"`
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	resolved := envVarPattern.ReplaceAllStringFunc(string(data), func(match string) string {
		varName := envVarPattern.FindStringSubmatch(match)[1]
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})

	var cfg Config
	if err := yaml.Unmarshal([]byte(resolved), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	baseDir := filepath.Dir(path)
	if err := cfg.validate(baseDir); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) validate(baseDir string) error {
	var errs []string

	if c.Slack.AppToken == "" {
		errs = append(errs, "slack.app_token is required")
	}
	if c.Slack.BotToken == "" {
		errs = append(errs, "slack.bot_token is required")
	}

	if c.Coordinator.Model == "" {
		errs = append(errs, "coordinator.model is required")
	}
	if c.Coordinator.Instruction == "" {
		errs = append(errs, "coordinator.instruction is required")
	}

	if c.PlaybooksDir == "" {
		errs = append(errs, "playbooks_dir is required")
	}

	mcpNames := make(map[string]bool)
	for _, mcp := range c.MCPServers {
		if mcp.Name == "" {
			errs = append(errs, "mcp_servers: each server must have a name")
		}
		if mcp.URL == "" {
			errs = append(errs, fmt.Sprintf("mcp_servers.%s: url is required", mcp.Name))
		}
		mcpNames[mcp.Name] = true
	}

	instructionPath := resolveRelativePath(baseDir, c.Coordinator.Instruction)
	if _, err := os.Stat(instructionPath); err != nil {
		errs = append(errs, fmt.Sprintf("coordinator instruction file not found: %s", instructionPath))
	}

	for _, agent := range c.Agents {
		if agent.Name == "" {
			errs = append(errs, "agents: each agent must have a name")
		}
		if agent.Model == "" {
			errs = append(errs, fmt.Sprintf("agents.%s: model is required", agent.Name))
		}
		if agent.Instruction == "" {
			errs = append(errs, fmt.Sprintf("agents.%s: instruction is required", agent.Name))
		} else {
			agentInstrPath := resolveRelativePath(baseDir, agent.Instruction)
			if _, err := os.Stat(agentInstrPath); err != nil {
				errs = append(errs, fmt.Sprintf("agents.%s: instruction file not found: %s", agent.Name, agentInstrPath))
			}
		}
		for _, tool := range agent.Tools {
			if !mcpNames[tool] {
				errs = append(errs, fmt.Sprintf("agents.%s: tool %q references undefined MCP server", agent.Name, tool))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config errors:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func resolveRelativePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, "..", path)
}
