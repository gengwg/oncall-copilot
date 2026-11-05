package slackio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/slack-go/slack"
)

func TestReplyExistsFindsBotReplyOnLaterPage(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("include_all_metadata") != "1" {
			http.Error(w, "metadata was not requested", http.StatusBadRequest)
			return
		}
		if r.Form.Get("cursor") == "next" {
			fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"2","thread_ts":"root","text":"result","bot_id":"B1","metadata":{"event_type":"oncall_copilot_reply","event_payload":{"receipt_id":"dc:1"}}}],"has_more":false}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"root","text":"brief"}],"has_more":true,"response_metadata":{"next_cursor":"next"}}`)
	}))
	defer server.Close()
	client := &Client{api: slack.New("test", slack.OptionAPIURL(server.URL+"/")), dm: "D1"}

	found, err := client.ReplyExists(context.Background(), "root", "result", "dc:1")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(paths) != 2 {
		t.Fatalf("requests=%v", paths)
	}
}

func TestReplyExistsDoesNotMatchOwnerText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"2","thread_ts":"root","text":"result","user":"U1"}],"has_more":false}`)
	}))
	defer server.Close()
	client := &Client{api: slack.New("test", slack.OptionAPIURL(server.URL+"/")), dm: "D1"}

	found, err := client.ReplyExists(context.Background(), "root", "result", "dc:1")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("owner's identical text matched the bot's durable receipt")
	}
}
