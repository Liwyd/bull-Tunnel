package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The bot is tested against a server that is right here: the messages it
// sends and the commands it answers.

// aConfig is the settings of the bot of the tests.
func aConfig(base string) model.Telegram {
	return model.Telegram{
		BotToken: "tok",
		ChatIDs:  []string{"123"},
		APIBase:  base,
		Enabled:  true,
	}
}

// TestTheBotSendsTheMessages: the events reach the chats.
func TestTheBotSendsTheMessages(t *testing.T) {
	sent := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			sent = append(sent, body)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer server.Close()

	bot := New(func() model.Telegram { return aConfig(server.URL) }, nil)
	if !bot.Active() {
		t.Error("the bot is not active, it must be")
	}
	if err := bot.SendTo("123", "hello", nil); err != nil {
		t.Fatalf("the message cannot be sent: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("the bot sent %d messages, it must send one", len(sent))
	}
	if sent[0]["chat_id"] != "123" || sent[0]["text"] != "hello" {
		t.Errorf("the message is %v, it must be the one of the panel", sent[0])
	}
	if sent[0]["parse_mode"] != "HTML" {
		t.Errorf("the message is %v, it must be in HTML", sent[0])
	}
}

// TestTheBotAnswersTheCommandsOfItsChats: a command of a chat that is not
// linked is refused, and the one of a linked chat is answered.
func TestTheBotAnswersTheCommandsOfItsChats(t *testing.T) {
	sent := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			sent = append(sent, body)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer server.Close()

	bot := New(func() model.Telegram { return aConfig(server.URL) },
		func(command string, args []string) (string, error) {
			return "the answer of " + command + " " + strings.Join(args, " "), nil
		})
	bot.handle(aConfig(server.URL), "123", "/status now")
	if len(sent) != 1 {
		t.Fatalf("the bot sent %d messages, it must send one", len(sent))
	}
	if sent[0]["text"] != "the answer of /status now" {
		t.Errorf("the answer is %v, it must be the one of the handler", sent[0]["text"])
	}

	sent = nil
	bot.handle(aConfig(server.URL), "999", "/start")
	if len(sent) != 1 {
		t.Fatalf("the bot sent %d messages, it must send one", len(sent))
	}
	if !strings.Contains(sent[0]["text"].(string), "999") {
		t.Errorf("the answer is %v, it must say which chat it is", sent[0]["text"])
	}
}

// TestTheBotSaysWhatWentWrong: a refusal of the API is an error the panel
// can show.
func TestTheBotSaysWhatWentWrong(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "chat not found"})
	}))
	defer server.Close()

	bot := New(func() model.Telegram { return aConfig(server.URL) }, nil)
	err := bot.SendTo("123", "hello", nil)
	if err == nil {
		t.Fatal("the bot says the message was sent, it must not")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("the bot said %q, it must say what the API said", err)
	}
}

// TestTheTextIsEscaped: the HTML of Telegram must not be broken by the text
// of an event.
func TestTheTextIsEscaped(t *testing.T) {
	if Escape(`<b>&</b>`) != "&lt;b&gt;&amp;&lt;/b&gt;" {
		t.Errorf("the text is escaped %q, the angle brackets and the ampersand must be", Escape(`<b>&</b>`))
	}
	if Escape(`say "hello"`) != `say "hello"` {
		t.Errorf("the text is escaped %q, the quotes must be left alone", Escape(`say "hello"`))
	}
}

// TestTheBotIsNotActiveWithoutAToken: a bot without a token says nothing.
func TestTheBotIsNotActiveWithoutAToken(t *testing.T) {
	bot := New(func() model.Telegram { return model.Telegram{Enabled: true} }, nil)
	if bot.Active() {
		t.Error("the bot is active without a token, it must not")
	}
	bot.Notify("hello") // it must not panic and must not send anything
}
