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
	ReplyIdentified(ctx context.Context, threadTS, text, receiptID string) error
	ReplyExists(ctx context.Context, threadTS, text, receiptID string) (bool, error)
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
	return c.postReply(ctx, threadTS, text, "")
}

func (c *Client) ReplyIdentified(ctx context.Context, threadTS, text, receiptID string) error {
	return c.postReply(ctx, threadTS, text, receiptID)
}

func (c *Client) postReply(ctx context.Context, threadTS, text, receiptID string) error {
	options := []slack.MsgOption{slack.MsgOptionText(text, false), slack.MsgOptionTS(threadTS), slack.MsgOptionDisableLinkUnfurl()}
	if receiptID != "" {
		options = append(options, slack.MsgOptionMetadata(slack.SlackMetadata{
			EventType: "oncall_copilot_reply", EventPayload: map[string]any{"receipt_id": receiptID},
		}))
	}
	_, _, err := c.api.PostMessageContext(ctx, c.dm, options...)
	return err
}

// ReplyExists reports whether this bot already posted the exact receipt in the
// thread. The metadata ID distinguishes identical text from different DC runs.
func (c *Client) ReplyExists(ctx context.Context, threadTS, text, receiptID string) (bool, error) {
	cursor := ""
	for {
		msgs, more, next, err := c.api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
			ChannelID: c.dm, Timestamp: threadTS, Cursor: cursor, Limit: 1000, IncludeAllMetadata: true,
		})
		if err != nil {
			return false, fmt.Errorf("slack: read thread replies: %w", err)
		}
		for _, msg := range msgs {
			id, _ := msg.Metadata.EventPayload["receipt_id"].(string)
			if msg.Timestamp != threadTS && msg.ThreadTimestamp == threadTS && msg.BotID != "" && msg.Text == text && receiptID != "" && id == receiptID {
				return true, nil
			}
		}
		if !more {
			return false, nil
		}
		if next == "" || next == cursor {
			return false, fmt.Errorf("slack: read thread replies: cursor did not advance")
		}
		cursor = next
	}
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
