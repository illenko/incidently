package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/illenko/incidently/internal/config"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type Message struct {
	Channel  string
	ThreadTS string
	UserID   string
	Text     string
}

type Gateway struct {
	api    *slack.Client
	socket *socketmode.Client
	botID  string
}

func NewGateway(cfg config.SlackConfig) *Gateway {
	api := slack.New(
		cfg.BotToken,
		slack.OptionAppLevelToken(cfg.AppToken),
	)

	socket := socketmode.New(api)

	return &Gateway{
		api:    api,
		socket: socket,
	}
}

func (g *Gateway) Run(ctx context.Context, handler func(msg Message)) {
	slog.Info("authenticating with Slack")
	authResp, err := g.api.AuthTest()
	if err != nil {
		slog.Error("slack auth test failed", "error", err)
		return
	}
	g.botID = authResp.UserID
	slog.Info("slack authenticated", "bot_user", authResp.User, "bot_id", g.botID, "team", authResp.Team)

	smHandler := socketmode.NewSocketmodeHandler(g.socket)

	smHandler.HandleEvents(slackevents.AppMention, func(evt *socketmode.Event, client *socketmode.Client) {
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			slog.Warn("unexpected event data type", "type", fmt.Sprintf("%T", evt.Data))
			return
		}
		client.Ack(*evt.Request)

		ev, ok := eventsAPIEvent.InnerEvent.Data.(*slackevents.AppMentionEvent)
		if !ok {
			slog.Warn("unexpected inner event type", "type", fmt.Sprintf("%T", eventsAPIEvent.InnerEvent.Data))
			return
		}

		threadTS := ev.ThreadTimeStamp
		if threadTS == "" {
			threadTS = ev.TimeStamp
		}

		text := stripBotMention(ev.Text, g.botID)

		slog.Debug("app mention received",
			"user", ev.User,
			"channel", ev.Channel,
			"thread", threadTS,
			"text", text,
		)

		handler(Message{
			Channel:  ev.Channel,
			ThreadTS: threadTS,
			UserID:   ev.User,
			Text:     text,
		})
	})

	slog.Info("starting socket mode event loop")

	go func() {
		<-ctx.Done()
		slog.Info("context cancelled, shutting down slack gateway")
	}()

	if err := smHandler.RunEventLoopContext(ctx); err != nil {
		slog.Error("socket mode event loop ended", "error", err)
	}

	slog.Info("slack gateway stopped")
}

func (g *Gateway) PostMessage(channel, threadTS, text string) error {
	_, _, err := g.api.PostMessage(
		channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		return fmt.Errorf("posting message: %w", err)
	}
	return nil
}

func stripBotMention(text, botID string) string {
	mention := fmt.Sprintf("<@%s>", botID)
	text = strings.Replace(text, mention, "", 1)
	return strings.TrimSpace(text)
}
