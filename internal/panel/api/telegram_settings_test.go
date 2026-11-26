package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/telegram"
)

// The Telegram bot is managed from the settings of the page: the token, the
// chats, the address and the proxy are saved as they are written, the token
// is never shown (and never lost to its mask), and the page says how the bot
// is doing without waiting for Telegram.

// aTelegram is a Telegram that knows the token "good" (the bot "mybot") and
// has no chat 9.
func aTelegram(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body := map[string]any{}
		json.NewDecoder(r.Body).Decode(&body)
		path := r.URL.Path
		switch {
		case !strings.HasPrefix(path, "/botgood/"):
			w.Write([]byte(`{"ok": false, "error_code": 401, "description": "Unauthorized"}`))
		case strings.HasSuffix(path, "/getMe"):
			w.Write([]byte(`{"ok": true, "result": {"username": "mybot"}}`))
		case strings.HasSuffix(path, "/sendMessage") && body["chat_id"] == "9":
			w.Write([]byte(`{"ok": false, "description": "Bad Request: chat not found"}`))
		default:
			w.Write([]byte(`{"ok": true, "result": {}}`))
		}
	}))
	t.Cleanup(fake.Close)
	return fake
}

// aPanelWithABot is a panel with a bot that reads its settings from the state.
func aPanelWithABot(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	return aServerWith(t, func(dir string, o *Options) {
		o.Bot = telegram.New(func() model.Telegram {
			o.Store.RLock()
			defer o.Store.RUnlock()
			return o.Store.Settings.TG()
		}, nil)
	})
}

// botLine is the status line of the settings page.
func botLine(t *testing.T, server *httptest.Server, cookie *http.Cookie) string {
	t.Helper()
	_, view := aGet(t, server, http.MethodGet, "/api/view", "", cookie)
	parts, _ := view["parts"].(map[string]any)
	body, _ := parts["set-body"].(string)
	at := strings.Index(body, `id="tg-status"`)
	if at < 0 {
		t.Fatalf("the settings page has no status line for the bot: %s", body)
	}
	return body[at : at+strings.Index(body[at:], "</p>")]
}

func TestTheSettingsManageTheBot(t *testing.T) {
	fake := aTelegram(t)
	panel, server := aPanelWithABot(t)
	cookie := aLogin(t, server)

	if line := botLine(t, server, cookie); !strings.Contains(line, "Not set up") {
		t.Errorf("a panel with no token says %q, it must say the bot is not set up", line)
	}

	// what the page saves when he types the token and the chat, and leaves
	body := `{"telegram": {"enabled": true, "bot_token": "good", "chat_ids": ["42", " -100123 ", ""],
		"api_base": "` + fake.URL + `", "proxy": ""}}`
	if status, answer := aGet(t, server, http.MethodPut, "/api/settings", body, cookie); status != http.StatusOK {
		t.Fatalf("the save is %d (%v), it must be 200", status, answer["detail"])
	}
	saved := panel.store.Settings.TG()
	if saved.BotToken != "good" || strings.Join(saved.ChatIDs, "|") != "42|-100123" || saved.APIBase != fake.URL || !saved.Enabled {
		t.Errorf("the bot was saved as %+v, it must hold what he wrote, the chats as a clean list", saved)
	}

	// the token is shown as its mask, on the page, in the form and in the settings
	_, public := aGet(t, server, http.MethodGet, "/api/settings", "", cookie)
	if got := public["telegram"].(map[string]any)["bot_token"]; got != mask {
		t.Errorf("the settings show the token as %v, they must show the mask", got)
	}
	_, view := aGet(t, server, http.MethodGet, "/api/view", "", cookie)
	if page := view["parts"].(map[string]any)["set-body"].(string); strings.Contains(page, `value="good"`) {
		t.Errorf("the settings page holds the token itself")
	}

	// the name of the bot is asked for in the background; the page shows it
	deadline := time.Now().Add(3 * time.Second)
	line := ""
	for time.Now().Before(deadline) {
		if line = botLine(t, server, cookie); strings.Contains(line, "@mybot") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(line, "Connected") || !strings.Contains(line, "@mybot") {
		t.Errorf("the status line is %q, it must say the bot is connected as @mybot", line)
	}

	// saving the page again with the mask in the field keeps the token
	keep := `{"telegram": {"bot_token": "` + mask + `", "chat_ids": ["42"]}}`
	aGet(t, server, http.MethodPut, "/api/settings", keep, cookie)
	if got := panel.store.Settings.TG().BotToken; got != "good" {
		t.Errorf("saving the mask left the token as %q, it must keep it", got)
	}

	// a switch that is off is said first
	aGet(t, server, http.MethodPut, "/api/settings", `{"telegram": {"enabled": false}}`, cookie)
	if line := botLine(t, server, cookie); !strings.Contains(line, "Off") {
		t.Errorf("a bot that is switched off says %q, it must say Off", line)
	}
	aGet(t, server, http.MethodPut, "/api/settings", `{"telegram": {"enabled": true}}`, cookie)

	// clearing the fields clears them: the chats, the proxy, then the token
	clear := `{"telegram": {"bot_token": "", "chat_ids": [], "proxy": ""}}`
	aGet(t, server, http.MethodPut, "/api/settings", clear, cookie)
	if cleared := panel.store.Settings.TG(); cleared.BotToken != "" || len(cleared.ChatIDs) != 0 || cleared.Proxy != "" {
		t.Errorf("clearing the fields left %+v", cleared)
	}
	if line := botLine(t, server, cookie); !strings.Contains(line, "Not set up") {
		t.Errorf("a bot with its token cleared says %q, it must say it is not set up", line)
	}
}

// An address the bot cannot use is refused with the reason, not dropped
// without a word while the field goes on showing it.
func TestAnAddressThatIsNotOneIsRefused(t *testing.T) {
	panel, server := aPanelWithABot(t)
	cookie := aLogin(t, server)
	for _, bad := range []string{"api.example.com", "ftp://x.example", "http://", "socks5://127.0.0.1:1080"} {
		status, answer := aGet(t, server, http.MethodPut, "/api/settings", `{"telegram": {"api_base": "`+bad+`"}}`, cookie)
		if status != http.StatusBadRequest || !strings.Contains(answer["detail"].(string), "http") {
			t.Errorf("the address %q is %d (%v), it must be refused with the reason", bad, status, answer["detail"])
		}
	}
	if got := panel.store.Settings.TG().APIBase; got != "https://api.telegram.org" {
		t.Errorf("the address is %q after the refusals, it must be the one it was", got)
	}
	if status, _ := aGet(t, server, http.MethodPut, "/api/settings", `{"telegram": {"api_base": "https://tg.example.com/"}}`, cookie); status != http.StatusOK {
		t.Errorf("a reverse proxy address is %d, it must be accepted", status)
	}
}

// The test button says what Telegram said.
func TestTheTestMessageReportsTheRealError(t *testing.T) {
	fake := aTelegram(t)
	panel, server := aPanelWithABot(t)
	cookie := aLogin(t, server)
	set := func(token string, chats ...string) {
		panel.store.Lock()
		panel.store.Settings.Telegram = map[string]any{"bot_token": token, "api_base": fake.URL, "enabled": true,
			"chat_ids": append([]any{}, toAny(chats)...)}
		panel.store.Unlock()
	}

	set("")
	status, answer := aGet(t, server, http.MethodPost, "/api/telegram/test", "{}", cookie)
	if status != http.StatusBadGateway || !strings.Contains(answer["detail"].(string), "token") {
		t.Errorf("a test with no token is %d (%v), it must say the token is not set", status, answer["detail"])
	}
	set("bad", "1")
	status, answer = aGet(t, server, http.MethodPost, "/api/telegram/test", "{}", cookie)
	if status != http.StatusBadGateway || answer["detail"] != "Unauthorized" {
		t.Errorf("a test with a wrong token is %d (%v), it must say Unauthorized", status, answer["detail"])
	}
	set("good", "9")
	status, answer = aGet(t, server, http.MethodPost, "/api/telegram/test", "{}", cookie)
	if status != http.StatusBadGateway || !strings.Contains(answer["detail"].(string), "chat not found") {
		t.Errorf("a test to a chat that is not there is %d (%v), it must say chat not found", status, answer["detail"])
	}
	set("good")
	status, answer = aGet(t, server, http.MethodPost, "/api/telegram/test", "{}", cookie)
	if status != http.StatusBadGateway || !strings.Contains(answer["detail"].(string), "Chat ID") {
		t.Errorf("a test with no chat is %d (%v), it must say there is no Chat ID", status, answer["detail"])
	}
	set("good", "42")
	if status, answer = aGet(t, server, http.MethodPost, "/api/telegram/test", "{}", cookie); status != http.StatusOK {
		t.Errorf("a good test is %d (%v), it must be 200", status, answer["detail"])
	}
}

func toAny(list []string) []any {
	out := make([]any, len(list))
	for i, one := range list {
		out[i] = one
	}
	return out
}
