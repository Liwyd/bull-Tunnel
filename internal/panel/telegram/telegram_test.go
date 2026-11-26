package telegram

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

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

// TestALongTextIsCutByCharactersAndStaysValid: a text is cut for Telegram by
// characters and not by bytes, and the HTML that is left is one Telegram can
// parse — a half tag, a half entity or a tag left open is a message the API
// refuses.
func TestALongTextIsCutByCharactersAndStaysValid(t *testing.T) {
	tail := "<b>x &amp; <code>y</code> &lt;z&gt;</b>" + strings.Repeat("&amp;<i>q</i> ", 200)
	for offset := 3900; offset <= 4010; offset++ {
		// letters of two bytes, so a cut by bytes lands inside one
		text := strings.Repeat("é", offset) + tail
		got := Truncate(text, 4000)
		if !utf8.ValidString(got) {
			t.Fatalf("offset %d: the text is not UTF-8 any more", offset)
		}
		if n := utf8.RuneCountInString(got); n > 4000 {
			t.Fatalf("offset %d: the text has %d characters, it must have at most 4000", offset, n)
		}
		if closing := closingTags(got); closing != "" {
			t.Fatalf("offset %d: the tags %q are left open", offset, closing)
		}
		if i := strings.LastIndex(got, "<"); i >= 0 && !strings.Contains(got[i:], ">") {
			t.Fatalf("offset %d: the text ends inside a tag: %q", offset, got[i:])
		}
		for i := 0; i < len(got); i++ {
			if got[i] != '&' {
				continue
			}
			end := strings.IndexByte(got[i:], ';')
			if end < 0 || end > 6 {
				t.Fatalf("offset %d: an entity is cut in the middle: %q", offset, got[i:])
			}
		}
	}
	// a text that fits is not touched
	if got := Truncate("<b>ok</b> é", 4000); got != "<b>ok</b> é" {
		t.Errorf("a short text became %q", got)
	}
}

// TestALongMessageReachesTelegramWholeAndShort: the text the API receives is
// valid UTF-8 and short enough.
func TestALongMessageReachesTelegramWholeAndShort(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		json.NewDecoder(r.Body).Decode(&body)
		got, _ = body["text"].(string)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer server.Close()
	bot := New(func() model.Telegram { return aConfig(server.URL) }, nil)
	if err := bot.SendTo("123", strings.Repeat("تونل ", 3000), nil); err != nil {
		t.Fatalf("the message cannot be sent: %v", err)
	}
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 4000 {
		t.Errorf("the API received %d characters (valid: %v), it must receive at most 4000 valid ones",
			utf8.RuneCountInString(got), utf8.ValidString(got))
	}
}

// aSocksServer is a SOCKS5 server for the tests: it remembers where it was
// asked to connect and passes the bytes on.
func aSocksServer(t *testing.T) (string, func() []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	var mu sync.Mutex
	asked := []string{}
	serve := func(conn net.Conn) {
		defer conn.Close()
		head := make([]byte, 2)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		io.ReadFull(conn, make([]byte, head[1]))
		conn.Write([]byte{5, 0})
		request := make([]byte, 4)
		if _, err := io.ReadFull(conn, request); err != nil {
			return
		}
		host := ""
		switch request[3] {
		case 1:
			ip := make([]byte, 4)
			io.ReadFull(conn, ip)
			host = net.IP(ip).String()
		case 3:
			size := make([]byte, 1)
			io.ReadFull(conn, size)
			name := make([]byte, size[0])
			io.ReadFull(conn, name)
			host = string(name)
		}
		port := make([]byte, 2)
		io.ReadFull(conn, port)
		target := net.JoinHostPort(host, strconv.Itoa(int(port[0])<<8|int(port[1])))
		mu.Lock()
		asked = append(asked, target)
		mu.Unlock()
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		defer upstream.Close()
		conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		go io.Copy(upstream, conn)
		io.Copy(conn, upstream)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serve(conn)
		}
	}()
	return listener.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, asked...)
	}
}

// TestTheProxyOfTheSettingsIsUsed: the proxy was a setting nothing read. The
// messages and the files go through it — an HTTP proxy and a SOCKS5 one —
// and a proxy that is changed is used from the next message on.
func TestTheProxyOfTheSettingsIsUsed(t *testing.T) {
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer telegram.Close()

	t.Run("http", func(t *testing.T) {
		var mu sync.Mutex
		seen := []string{}
		proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.URL.String())
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
		}))
		defer proxyServer.Close()
		cfg := aConfig("http://telegram.invalid")
		cfg.Proxy = proxyServer.URL
		bot := New(func() model.Telegram { return cfg }, nil)
		if err := bot.SendTo("123", "hello", nil); err != nil {
			t.Fatalf("the message did not go through the proxy: %v", err)
		}
		if sent, err := bot.SendFile("backup.tmbk", []byte("data"), "caption", nil); err != nil || sent != 1 {
			t.Fatalf("the file did not go through the proxy: sent %d, %v", sent, err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(seen) != 2 || !strings.HasSuffix(seen[0], "/sendMessage") || !strings.HasSuffix(seen[1], "/sendDocument") {
			t.Errorf("the proxy saw %v, it must see the message and the file", seen)
		}
	})

	t.Run("socks5", func(t *testing.T) {
		address, asked := aSocksServer(t)
		cfg := aConfig(telegram.URL)
		cfg.Proxy = "socks5://" + address
		bot := New(func() model.Telegram { return cfg }, nil)
		if err := bot.SendTo("123", "hello", nil); err != nil {
			t.Fatalf("the message did not go through the proxy: %v", err)
		}
		if _, err := bot.SendFile("backup.tmbk", []byte("data"), "caption", nil); err != nil {
			t.Fatalf("the file did not go through the proxy: %v", err)
		}
		want := strings.TrimPrefix(telegram.URL, "http://")
		got := asked()
		if len(got) == 0 {
			t.Fatal("the SOCKS5 proxy was never asked to connect")
		}
		for _, target := range got {
			if target != want {
				t.Errorf("the proxy was asked for %q, it must be asked for %q", target, want)
			}
		}

		// the proxy is taken off in the settings: the next message goes straight
		before := len(asked())
		cfg.Proxy = ""
		if err := bot.SendTo("123", "again", nil); err != nil {
			t.Fatalf("the message failed without the proxy: %v", err)
		}
		if after := len(asked()); after != before {
			t.Errorf("the proxy was used after it was taken out of the settings (%d, then %d connections)", before, after)
		}
	})

	t.Run("a proxy that cannot be used is an error", func(t *testing.T) {
		cfg := aConfig(telegram.URL)
		cfg.Proxy = "ftp://127.0.0.1:21"
		bot := New(func() model.Telegram { return cfg }, nil)
		if err := bot.SendTo("123", "hello", nil); err == nil {
			t.Error("the message was sent through a proxy the bot does not speak")
		}
	})
}
