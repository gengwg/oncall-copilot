// Package slackio posts DMs to the owner and listens, over Socket Mode, for
// their replies. Only the owner's messages are passed on.
package slackio

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// Poster is what the agent needs to talk; tests use a fake.
type Poster interface {
	PostDM(ctx context.Context, text string) (ts string, err error)
	Reply(ctx context.Context, threadTS, text string) error
}

// Inbound is one message from the owner. ThreadTS is empty for a top-level DM.
type Inbound struct {
	ThreadTS string
	TS       string
	Text     string
}

type Client struct {
	api   *slack.Client
	sm    *socketmode.Client
	owner string
	dm    string
	log   *log.Logger
}

// New opens the DM channel with the owner so the first post needs no lookup.
func New(ctx context.Context, botToken, appToken, owner string, lg *log.Logger) (*Client, error) {
	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	ch, _, _, err := api.OpenConversationContext(ctx, &slack.OpenConversationParameters{Users: []string{owner}})
	if err != nil {
		return nil, fmt.Errorf("slack: open DM with %s: %w", owner, err)
	}
	return &Client{api: api, sm: socketmode.New(api), owner: owner, dm: ch.ID, log: lg}, nil
}

func (c *Client) PostDM(ctx context.Context, text string) (string, error) {
	_, ts, err := c.api.PostMessageContext(ctx, c.dm, slack.MsgOptionText(text, false), slack.MsgOptionDisableLinkUnfurl())
	return ts, err
}

func (c *Client) Reply(ctx context.Context, threadTS, text string) error {
	_, _, err := c.api.PostMessageContext(ctx, c.dm, slack.MsgOptionText(text, false), slack.MsgOptionTS(threadTS), slack.MsgOptionDisableLinkUnfurl())
	return err
}

// Listen runs the Socket Mode loop until ctx ends, calling handle for each
// message the owner types in the DM. Bot and system messages are dropped.
func (c *Client) Listen(ctx context.Context, handle func(Inbound)) error {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-c.sm.Events:
				if !ok {
					return
				}
				c.dispatch(evt, handle)
			}
		}
	}()
	return c.sm.RunContext(ctx)
}

func (c *Client) dispatch(evt socketmode.Event, handle func(Inbound)) {
	if evt.Type != socketmode.EventTypeEventsAPI {
		return
	}
	api, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	if evt.Request != nil {
		c.sm.Ack(*evt.Request)
	}
	msg, ok := api.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok || msg.ChannelType != "im" || msg.Channel != c.dm {
		return
	}
	if msg.BotID != "" || msg.SubType != "" || msg.User != c.owner {
		return
	}
	handle(Inbound{ThreadTS: msg.ThreadTimeStamp, TS: msg.TimeStamp, Text: strings.TrimSpace(msg.Text)})
}
