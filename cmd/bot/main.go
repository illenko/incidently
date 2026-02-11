package main

import (
	"flag"
	"log"

	"github.com/illenko/incidently/internal/config"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("config loaded successfully")
	log.Printf("coordinator model: %s", cfg.Coordinator.Model)
	log.Printf("MCP servers: %d", len(cfg.MCPServers))
	for _, mcp := range cfg.MCPServers {
		log.Printf("  - %s: %s", mcp.Name, mcp.URL)
	}
	log.Printf("agents: %d", len(cfg.Agents))
	for _, agent := range cfg.Agents {
		log.Printf("  - %s (model: %s, tools: %v)", agent.Name, agent.Model, agent.Tools)
	}
	log.Printf("playbooks dir: %s", cfg.PlaybooksDir)
}
