package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/illenko/incidently/internal/agent"
	"github.com/illenko/incidently/internal/config"
	islack "github.com/illenko/incidently/internal/slack"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if err := run(); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	slog.Info("config loaded",
		"coordinator_model", cfg.Coordinator.Model,
		"mcp_servers", len(cfg.MCPServers),
		"agents", len(cfg.Agents),
		"playbooks_dir", cfg.PlaybooksDir,
	)
	for _, mcp := range cfg.MCPServers {
		slog.Info("mcp server configured", "name", mcp.Name, "url", mcp.URL)
	}
	for _, a := range cfg.Agents {
		slog.Info("agent configured", "name", a.Name, "model", a.Model, "tools", a.Tools)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	playbooks, err := agent.LoadPlaybooks(cfg.PlaybooksDir)
	if err != nil {
		return fmt.Errorf("loading playbooks: %w", err)
	}
	slog.Info("playbooks loaded", "count", len(playbooks))

	svc, err := agent.NewService(ctx, cfg, playbooks)
	if err != nil {
		return fmt.Errorf("creating agent service: %w", err)
	}
	defer svc.Close()

	gw := islack.NewGateway(cfg.Slack)

	slog.Info("starting slack gateway")
	gw.Run(ctx, func(msg islack.Message) {
		slog.Info("message received",
			"user", msg.UserID,
			"channel", msg.Channel,
			"thread", msg.ThreadTS,
			"text", msg.Text,
		)

		if err := gw.PostMessage(msg.Channel, msg.ThreadTS, "Investigating..."); err != nil {
			slog.Error("failed to send initial progress", "error", err, "thread", msg.ThreadTS)
		}

		var mu sync.Mutex
		lastProgress := time.Now()
		onProgress := func(text string) {
			mu.Lock()
			defer mu.Unlock()
			if time.Since(lastProgress) < 5*time.Second {
				return
			}
			lastProgress = time.Now()
			slog.Debug("sending progress", "thread", msg.ThreadTS, "text", text)
			if err := gw.PostMessage(msg.Channel, msg.ThreadTS, text); err != nil {
				slog.Error("failed to send progress", "error", err, "thread", msg.ThreadTS)
			}
		}

		response, err := svc.HandleMessage(ctx, msg.UserID, msg.ThreadTS, msg.Text, onProgress)
		if err != nil {
			slog.Error("agent error", "error", err, "thread", msg.ThreadTS, "user", msg.UserID)
			if postErr := gw.PostMessage(msg.Channel, msg.ThreadTS, "Sorry, something went wrong during analysis."); postErr != nil {
				slog.Error("failed to send error message", "error", postErr, "thread", msg.ThreadTS)
			}
			return
		}

		slog.Info("sending final response", "thread", msg.ThreadTS, "length", len(response))
		if err := gw.PostMessage(msg.Channel, msg.ThreadTS, response); err != nil {
			slog.Error("failed to send response", "error", err, "thread", msg.ThreadTS)
		}
	})

	return nil
}
