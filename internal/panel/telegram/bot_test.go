package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The bot as the operator uses it: its settings change while it runs, a
// chat that is not linked asks for its number, and the page asks how it is
// doing. A Telegram that is right here stands in for the real one, and the
// waits of the bot are shortened so that nothing sleeps for long.

// call is one request the fake Telegram got.
type call struct {
	Token  string
	Method string
	Body   map[string]any
}

// fakeTelegram is a Telegram API that keeps what it was asked and answers
// from what the test says. The token "good" is the one it knows.
type fakeTelegram struct {
	*httptest.Server
	mu       sync.Mutex
	calls    []call
	updates  map[string][]any // what getUpdates gives for a token, once
	blocking map[string]bool  // a token whose getUpdates waits until it is dropped
	badChats map[string]bool  // the chats it cannot write to
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	fake := &fakeTelegram{updates: map[string][]any{}, blocking: map[string]bool{}, badChats: map[string]bool{}}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/bot"), "/")
	token, method := parts[0], parts[len(parts)-1]
	body := map[string]any{}
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.calls = append(f.calls, call{token, method, body})
	known := token == "good" || token == "other"
	blocking := f.blocking[token]
	var updates []any
	if method == "getUpdates" {
		updates = f.updates[token]
		delete(f.updates, token)
	}
	chatBad := f.badChats[stringOf(body["chat_id"])]
	f.mu.Unlock()

	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	switch {
	case !known:
		w.WriteHeader(http.StatusUnauthorized)
		reply(map[string]any{"ok": false, "error_code": 401, "description": "Unauthorized"})
	case method == "getMe":
		reply(map[string]any{"ok": true, "result": map[string]any{"username": token + "_bot"}})
	case method == "getUpdates" && blocking:
		<-r.Context().Done() // the poll is dropped by the bot
	case method == "getUpdates":
		if updates == nil {
			time.Sleep(5 * time.Millisecond) // a poll that finds nothing
			updates = []any{}
		}
		reply(map[string]any{"ok": true, "result": updates})
	case method == "sendMessage" && chatBad:
		reply(map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: chat not found"})
	default:
		reply(map[string]any{"ok": true, "result": map[string]any{}})
	}
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

// of is the calls of one method, for one token ("" is any).
func (f *fakeTelegram) of(method, token string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []call{}
	for _, one := range f.calls {
		if (method == "" || one.Method == method) && (token == "" || one.Token == token) {
			out = append(out, one)
		}
	}
	return out
}

// settings is a config the test can change while the bot runs.
type settings struct {
	mu  sync.Mutex
	cfg model.Telegram
}

func (s *settings) get() model.Telegram {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *settings) set(change func(*model.Telegram)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(&s.cfg)
}

// quick is a bot whose waits are short.
func quick(get func() model.Telegram, handler Handler) *Bot {
	bot := New(get, handler)
	bot.idle, bot.retry, bot.pace = 5*time.Millisecond, 5*time.Millisecond, time.Millisecond
	bot.backoff = func(int) time.Duration { return time.Millisecond }
	return bot
}

// eventually waits for a condition, for as long as a slow machine needs.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("it did not happen: %s", what)
}

func aStart(chatID any, text string) []any {
	return []any{map[string]any{"update_id": float64(7), "message": map[string]any{
		"text": text, "chat": map[string]any{"id": chatID}}}}
}

// A bot that started without a token, and was off, takes the settings the
// operator enters afterwards — no restart — and answers /start with the
// number of the chat even though no chat is set yet. The number is the
// number, not the floating point notation of it.
func TestABotThatStartedUnsetWorksOnceItIsSetUp(t *testing.T) {
	fake := newFakeTelegram(t)
	config := &settings{cfg: model.Telegram{APIBase: fake.URL, Enabled: false}}
	bot := quick(config.get, nil)
	bot.Start()
	defer bot.Stop()

	time.Sleep(30 * time.Millisecond)
	if n := len(fake.of("", "")); n != 0 {
		t.Fatalf("the bot called Telegram %d times with no token and switched off", n)
	}

	fake.mu.Lock()
	fake.updates["good"] = aStart(float64(123456789), "/start")
	fake.mu.Unlock()
	config.set(func(c *model.Telegram) { c.BotToken, c.Enabled = "good", true })
	bot.Reload()

	eventually(t, "the answer to /start", func() bool { return len(fake.of("sendMessage", "good")) > 0 })
	reply := fake.of("sendMessage", "good")[0].Body
	if reply["chat_id"] != "123456789" {
		t.Errorf("the answer went to %v, it must go to the chat that asked", reply["chat_id"])
	}
	if text, _ := reply["text"].(string); !strings.Contains(text, "<code>123456789</code>") {
		t.Errorf("the answer is %q, it must hold the number of the chat as it is", text)
	}
}

// A chat that is not linked gets the number to /start and nothing else: a
// command of such a chat is not run and not answered, with chats set or not.
func TestAnUnknownChatOnlyLearnsItsNumber(t *testing.T) {
	for _, chats := range [][]string{nil, {"42"}} {
		fake := newFakeTelegram(t)
		ran := []string{}
		bot := quick(func() model.Telegram {
			return model.Telegram{BotToken: "good", APIBase: fake.URL, Enabled: true, ChatIDs: chats}
		}, func(command string, args []string) (string, error) {
			ran = append(ran, command)
			return "done", nil
		})
		cfg := bot.GetConfig()
		for _, command := range []string{"/status", "/check", "/backup now", "/links", "hello"} {
			bot.handle(cfg, "999", command)
		}
		if len(ran) != 0 || len(fake.of("sendMessage", "")) != 0 {
			t.Fatalf("chats %v: an unknown chat got the commands run %v and %d messages", chats, ran, len(fake.of("sendMessage", "")))
		}
		bot.handle(cfg, "999", "/start@the_bot")
		sent := fake.of("sendMessage", "")
		if len(sent) != 1 || !strings.Contains(sent[0].Body["text"].(string), "999") || len(ran) != 0 {
			t.Fatalf("chats %v: /start answered %v (commands run: %v), it must say the number and nothing else", chats, sent, ran)
		}
	}
}

// The number of a chat is read exactly, whatever its size.
func TestTheNumberOfAChatIsRead(t *testing.T) {
	for in, want := range map[any]string{
		float64(123456789): "123456789", float64(-1001234567890): "-1001234567890",
		float64(5): "5", "42": "42", nil: "",
	} {
		if got := idOf(in); got != want {
			t.Errorf("the chat %v is read as %q, want %q", in, got, want)
		}
	}
}

// A poll that is under way with the old token is dropped when the settings
// change, and the new token is used at once. The offset of the old bot is
// not carried over: it would hide every message of the new one.
func TestANewTokenIsUsedAtOnce(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.blocking["good"] = true // before the bot starts: nothing else reads it yet
	config := &settings{cfg: model.Telegram{BotToken: "good", APIBase: fake.URL, Enabled: true}}
	bot := quick(config.get, nil)
	bot.Start()
	defer bot.Stop()
	eventually(t, "the first poll", func() bool { return len(fake.of("getUpdates", "good")) > 0 })

	bot.offset = 1001 // what the old bot had got to (the poller is blocked, nothing else touches it)
	config.set(func(c *model.Telegram) { c.BotToken = "other" })
	bot.Reload()

	eventually(t, "a poll with the new token", func() bool { return len(fake.of("getUpdates", "other")) > 0 })
	if offset := fake.of("getUpdates", "other")[0].Body["offset"]; offset != float64(0) {
		t.Errorf("the new bot was asked from offset %v, it must be 0", offset)
	}
}

// A bot that is switched off does not call Telegram, and takes the switch
// as soon as it is turned on.
func TestTheSwitchIsTakenWithoutARestart(t *testing.T) {
	fake := newFakeTelegram(t)
	config := &settings{cfg: model.Telegram{BotToken: "good", APIBase: fake.URL, Enabled: false}}
	bot := quick(config.get, nil)
	bot.Start()
	defer bot.Stop()
	time.Sleep(30 * time.Millisecond)
	if n := len(fake.of("getUpdates", "")); n != 0 {
		t.Fatalf("a bot that is off polled Telegram %d times", n)
	}
	config.set(func(c *model.Telegram) { c.Enabled = true })
	bot.Reload()
	eventually(t, "polling", func() bool { return len(fake.of("getUpdates", "good")) > 0 })
}

// A poll that fails is tried again, and the failure is what the page shows
// until a poll works.
func TestAFailedPollIsTriedAgain(t *testing.T) {
	fake := newFakeTelegram(t)
	config := &settings{cfg: model.Telegram{BotToken: "wrong", APIBase: fake.URL, Enabled: true}}
	bot := quick(config.get, nil)
	bot.Start()
	defer bot.Stop()
	eventually(t, "a second poll after the first failed", func() bool { return len(fake.of("getUpdates", "wrong")) >= 2 })
	eventually(t, "the error", func() bool { return bot.Status(config.get()).Error == "Unauthorized" })

	config.set(func(c *model.Telegram) { c.BotToken = "good" })
	bot.Reload()
	eventually(t, "the name of the bot", func() bool { return bot.Status(config.get()).Username == "good_bot" })
	if status := bot.Status(config.get()); status.Error != "" || status.OKAt == 0 {
		t.Errorf("the status after a good poll is %+v, it must hold no error and the time of a call that worked", status)
	}
}

// The status is read without waiting: the name of the bot is asked for in
// the background, once, and the pages are told when it is known.
func TestTheStatusDoesNotWaitForTheNetwork(t *testing.T) {
	fake := newFakeTelegram(t)
	cfg := model.Telegram{BotToken: "good", APIBase: fake.URL, Enabled: true}
	bot := quick(func() model.Telegram { return cfg }, nil)
	told := make(chan struct{}, 8)
	bot.SetOnStatus(func() { told <- struct{}{} })

	if status := bot.Status(cfg); status.Username != "" || !status.Checking {
		t.Fatalf("the first status is %+v, it must be 'checking' with no name yet", status)
	}
	select {
	case <-told:
	case <-time.After(3 * time.Second):
		t.Fatal("the pages were not told that the name of the bot is known")
	}
	eventually(t, "the name", func() bool { return bot.Status(cfg).Username == "good_bot" })
	for i := 0; i < 5; i++ {
		bot.Status(cfg)
	}
	if n := len(fake.of("getMe", "")); n != 1 {
		t.Errorf("the name of the bot was asked for %d times, it must be asked once per token", n)
	}
	if status := bot.Status(model.Telegram{}); status.Username != "" || status.Error != "" {
		t.Errorf("a bot with no token has the status %+v, it must have nothing to say", status)
	}
}

// A failure is said as it was, and without the token: the address of a
// call holds it, and the status ends up on a page.
func TestAnErrorNeverHoldsTheToken(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	url := gone.URL
	gone.Close()
	cfg := model.Telegram{BotToken: "123456:SECRET-token", APIBase: url, Enabled: true, ChatIDs: []string{"1"}}
	bot := quick(func() model.Telegram { return cfg }, nil)

	err := bot.Test(&cfg)
	if err == nil {
		t.Fatal("a test against an address that is gone worked")
	}
	if strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "cannot reach Telegram") {
		t.Errorf("the error is %q, it must say Telegram cannot be reached and not hold the token", err)
	}
	if status := bot.Status(cfg); strings.Contains(status.Error, "SECRET") {
		t.Errorf("the status says %q, it must not hold the token", status.Error)
	}
}

// The test says what is wrong, not that it worked.
func TestTheTestMessageSaysWhatIsWrong(t *testing.T) {
	fake := newFakeTelegram(t)
	bot := quick(func() model.Telegram { return model.Telegram{} }, nil)
	cfg := model.Telegram{BotToken: "", APIBase: fake.URL, ChatIDs: []string{"1"}}

	if err := bot.Test(&cfg); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("without a token the test says %v, it must say the token is missing", err)
	}
	cfg.BotToken = "wrong"
	if err := bot.Test(&cfg); err == nil || err.Error() != "Unauthorized" {
		t.Errorf("with a token Telegram does not know the test says %v, it must say Unauthorized", err)
	}
	cfg.BotToken, cfg.ChatIDs = "good", nil
	if err := bot.Test(&cfg); err == nil || !strings.Contains(err.Error(), "Chat ID") || !strings.Contains(err.Error(), "good_bot") {
		t.Errorf("with no chat the test says %v, it must say there is no Chat ID, and which bot answered", err)
	}
	if n := len(fake.of("sendMessage", "")); n != 0 {
		t.Errorf("the test sent %d messages with no chat to send them to", n)
	}
	fake.badChats["2"] = true
	cfg.ChatIDs = []string{"1", "2"}
	err := bot.Test(&cfg)
	if err == nil || !strings.Contains(err.Error(), "chat 2") || !strings.Contains(err.Error(), "chat not found") || strings.Contains(err.Error(), "chat 1") {
		t.Errorf("with a chat that is not found the test says %v, it must name that chat and say why", err)
	}
	cfg.ChatIDs = []string{"1"}
	if err := bot.Test(&cfg); err != nil {
		t.Errorf("a good test failed: %v", err)
	}
}

// A message that cannot be sent is tried three times, with a wait between
// the tries and none after the last, and the status says why.
func TestAMessageThatFailsIsRetriedAndReported(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.badChats["1"] = true
	config := &settings{cfg: model.Telegram{BotToken: "good", APIBase: fake.URL, Enabled: true, ChatIDs: []string{"1"}}}
	bot := quick(config.get, nil)
	var waits atomic.Int32
	bot.backoff = func(int) time.Duration { waits.Add(1); return time.Millisecond }
	bot.Start()
	defer bot.Stop()

	bot.Notify("hello")
	eventually(t, "three tries", func() bool { return len(fake.of("sendMessage", "")) == 3 })
	eventually(t, "the reason", func() bool { return strings.Contains(bot.Status(config.get()).Error, "chat not found") })
	if n := waits.Load(); n != 2 {
		t.Errorf("the bot waited %d times between 3 tries, it must wait twice", n)
	}

	// the chat is corrected: the old reason is not the news any more
	fake.mu.Lock()
	delete(fake.badChats, "1")
	fake.mu.Unlock()
	bot.Notify("again")
	eventually(t, "a message that works", func() bool { return bot.Status(config.get()).Error == "" })
}
