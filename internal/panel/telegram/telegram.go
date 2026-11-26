// Package telegram is the panel's bot: it pushes every event to the
// configured chats and answers a few commands (/status, /links, /domains,
// /check, /help).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"golang.org/x/net/proxy"
)

// Icon is the sign of a level of an event.
var Icon = map[string]string{
	"info":    "ℹ️",
	"success": "✅",
	"warning": "⚠️",
	"error":   "🚨",
}

// Handler answers a command of the bot.
type Handler func(command string, args []string) (string, error)

// Error says that the bot could not do what it was asked.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// Bot is the panel's bot.
type Bot struct {
	// GetConfig gives the settings of the bot when they are needed.
	GetConfig func() model.Telegram
	// Handler answers the commands of the chats.
	Handler Handler

	queue  chan string
	offset int
	// mu guards the transport and the proxy it was made for.
	mu sync.Mutex
	// transport is how the bot reaches Telegram, and proxyKey is the proxy it
	// was made for: a proxy that is changed in the settings makes a new one.
	transport *http.Transport
	proxyKey  string
	stopped   chan struct{}
	once      sync.Once

	// How long the bot waits. They are fields so that a test does not have
	// to wait for them: idle is the wait of a bot that is off or has no
	// token, retry the wait after a poll that failed, pace the pause between
	// two messages (Telegram's rate limit), and backoff the wait before
	// attempt n+1 of a message that failed.
	idle    time.Duration
	retry   time.Duration
	pace    time.Duration
	backoff func(attempt int) time.Duration

	// The poll that is under way, so that a change of the settings can drop
	// it instead of waiting for it (it is a long poll: up to 25 seconds).
	// base ends with the bot, wake ends the wait of a poller that sleeps.
	base       context.Context
	stopBase   context.CancelFunc
	wake       chan struct{}
	pollMu     sync.Mutex
	pollCancel context.CancelFunc
	loaded     string // the settings the poller was last told about

	// What the bot knows about its own health, kept in the memory: Status
	// reads it without asking the network.
	healthMu sync.Mutex
	health   health
	onStatus func()
}

// New returns a bot.
func New(getConfig func() model.Telegram, handler Handler) *Bot {
	base, stop := context.WithCancel(context.Background())
	return &Bot{
		GetConfig: getConfig,
		Handler:   handler,
		queue:     make(chan string, 500),
		stopped:   make(chan struct{}),
		idle:      5 * time.Second,
		retry:     10 * time.Second,
		pace:      400 * time.Millisecond,
		backoff:   func(attempt int) time.Duration { return time.Duration(3*(attempt+1)) * time.Second },
		base:      base,
		stopBase:  stop,
		wake:      make(chan struct{}, 1),
	}
}

// Active tells whether the bot is configured and switched on.
func (b *Bot) Active() bool {
	cfg := b.GetConfig()
	return cfg.Enabled && cfg.BotToken != ""
}

// transportFor is how the bot reaches Telegram through a proxy.
//
// The proxy is an URL. "http://" and "https://" proxies are used the way the
// standard library uses them; "socks5://[user:password@]host:port" is dialled
// through the SOCKS5 server, which is how a panel that sits where Telegram is
// blocked reaches it. An address without a scheme is taken for an HTTP proxy.
// Without a proxy the bot uses the proxy of the environment, as a plain HTTP
// client does.
func transportFor(proxyURL string) (*http.Transport, error) {
	direct := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         direct.DialContext,
		MaxIdleConns:        4,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 20 * time.Second,
	}
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return transport, nil
	}
	if !strings.Contains(proxyURL, "://") {
		proxyURL = "http://" + proxyURL
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, &Error{Message: fmt.Sprintf("the proxy %q cannot be used", proxyURL)}
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsed)
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(parsed, direct)
		if err != nil {
			return nil, &Error{Message: fmt.Sprintf("the proxy cannot be used: %v", err)}
		}
		// the address is given to the SOCKS5 server as it is, a name too:
		// the proxy resolves it, which is the point when the resolver of the
		// machine is the one that is censored
		transport.Proxy = nil
		if withContext, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = withContext.DialContext
		} else {
			transport.DialContext = func(ctx context.Context, network string, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		}
	default:
		return nil, &Error{Message: fmt.Sprintf("the proxy scheme %q is not one the bot speaks (http, https, socks5)", parsed.Scheme)}
	}
	return transport, nil
}

// httpClient is the client the bot talks to Telegram with, through the proxy
// of the settings. The timeout is the one of the Python panel: forty seconds
// for a call, and the caller says how long a file may take.
func (b *Bot) httpClient(cfg model.Telegram, timeout time.Duration) (*http.Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.transport == nil || b.proxyKey != cfg.Proxy {
		transport, err := transportFor(cfg.Proxy)
		if err != nil {
			return nil, err
		}
		if b.transport != nil {
			b.transport.CloseIdleConnections()
		}
		b.transport, b.proxyKey = transport, cfg.Proxy
	}
	return &http.Client{Timeout: timeout, Transport: b.transport}, nil
}

// defaultAPI is the address of Telegram's own API.
const defaultAPI = "https://api.telegram.org"

// url is the address of a method of the Telegram API.
func (b *Bot) url(cfg model.Telegram, method string) string {
	base := strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/")
	if base == "" {
		base = defaultAPI
	}
	return fmt.Sprintf("%s/bot%s/%s", base, cfg.BotToken, method)
}

// reach is the error of a call that did not get an answer, as the operator
// reads it. The error of the HTTP client holds the whole address of the call,
// and the address holds the token of the bot: it must not end up on a page,
// in a log or in a message.
func reach(err error, cfg model.Telegram) error {
	message := err.Error()
	var failed *url.Error
	if errors.As(err, &failed) {
		message = failed.Err.Error()
	}
	if token := strings.TrimSpace(cfg.BotToken); token != "" {
		message = strings.ReplaceAll(message, token, "…")
	}
	return &Error{Message: "cannot reach Telegram: " + message}
}

// Call asks the Telegram API something.
func (b *Bot) Call(method string, cfg *model.Telegram, params map[string]any) (any, error) {
	return b.callContext(context.Background(), method, cfg, params)
}

// callContext is Call that can be dropped: the context ends the request.
// What came of it (the name of the bot, or why it failed) is written down
// for Status, except when the call was dropped on purpose.
func (b *Bot) callContext(ctx context.Context, method string, cfg *model.Telegram, params map[string]any) (any, error) {
	if cfg == nil {
		current := b.GetConfig()
		cfg = &current
	}
	result, err := b.request(ctx, method, *cfg, params)
	if ctx.Err() == nil {
		b.note(*cfg, method, result, err)
	}
	return result, err
}

// request is one call, and nothing else.
func (b *Bot) request(ctx context.Context, method string, cfg model.Telegram, params map[string]any) (any, error) {
	body, _ := json.Marshal(params)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url(cfg, method), bytes.NewReader(body))
	if err != nil {
		return nil, reach(err, cfg)
	}
	request.Header.Set("Content-Type", "application/json")
	client, err := b.httpClient(cfg, 40*time.Second)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, reach(err, cfg)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var answer struct {
		OK          bool   `json:"ok"`
		Result      any    `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, &Error{Message: fmt.Sprintf("HTTP %d", response.StatusCode)}
	}
	if !answer.OK {
		message := answer.Description
		if message == "" {
			message = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return nil, &Error{Message: message}
	}
	return answer.Result, nil
}

// Notify puts a message in the queue of the bot. The queue is bounded: a
// Telegram that is down for a while must not fill the memory of the panel.
func (b *Bot) Notify(text string) {
	if !b.Active() {
		return
	}
	select {
	case b.queue <- text:
	default:
		// the queue is full: the message is dropped, and the panel says
		// so in its own log.
	}
}

// SendTo sends a message to a chat.
func (b *Bot) SendTo(chatID string, text string, cfg *model.Telegram) error {
	text = Truncate(text, 4000)
	_, err := b.Call("sendMessage", cfg, map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	})
	return err
}

// SendFile sends a file to every chat; it returns how many got it.
func (b *Bot) SendFile(name string, blob []byte, caption string, cfg *model.Telegram) (int, error) {
	if cfg == nil {
		current := b.GetConfig()
		cfg = &current
	}
	if cfg.BotToken == "" || len(cfg.ChatIDs) == 0 {
		return 0, &Error{Message: "the Telegram bot or the Chat ID is not configured"}
	}
	caption = Truncate(caption, 1000)
	sent := 0
	var last error
	for _, chat := range cfg.ChatIDs {
		for attempt := 0; attempt < 3; attempt++ {
			err := b.sendDocument(*cfg, chat, name, blob, caption)
			if err == nil {
				sent++
				break
			}
			last = err
			if attempt < 2 {
				time.Sleep(b.backoff(attempt))
			}
		}
	}
	if sent == 0 && last != nil {
		return 0, last
	}
	return sent, nil
}

// sendDocument sends one file to one chat, and writes down how it went.
func (b *Bot) sendDocument(cfg model.Telegram, chatID string, name string, blob []byte, caption string) error {
	err := b.postDocument(cfg, chatID, name, blob, caption)
	b.note(cfg, "sendDocument", nil, err)
	return err
}

// postDocument is the request that sends a file.
func (b *Bot) postDocument(cfg model.Telegram, chatID string, name string, blob []byte, caption string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{"chat_id": chatID, "caption": caption, "parse_mode": "HTML"}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return err
		}
	}
	part, err := writer.CreateFormFile("document", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(blob); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, b.url(cfg, "sendDocument"), &body)
	if err != nil {
		return reach(err, cfg)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	client, err := b.httpClient(cfg, 120*time.Second)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return reach(err, cfg)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var answer struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return &Error{Message: fmt.Sprintf("HTTP %d", response.StatusCode)}
	}
	if !answer.OK {
		message := answer.Description
		if message == "" {
			message = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return &Error{Message: message}
	}
	return nil
}

// Test asks the bot who it is and says so in every chat. It fails with the
// reason when it cannot: the token Telegram does not know, a chat the bot
// is not in, or no chat to say it in at all.
func (b *Bot) Test(cfg *model.Telegram) error {
	if cfg == nil {
		current := b.GetConfig()
		cfg = &current
	}
	if strings.TrimSpace(cfg.BotToken) == "" {
		return &Error{Message: "the bot token is not set"}
	}
	answer, err := b.Call("getMe", cfg, map[string]any{})
	if err != nil {
		return err
	}
	me, _ := answer.(map[string]any)
	username, _ := me["username"].(string)
	if len(cfg.ChatIDs) == 0 {
		return &Error{Message: fmt.Sprintf("the bot @%s answers, but no Chat ID is set: send /start to it and enter the number it replies with", username)}
	}
	failed := []string{}
	for _, chat := range cfg.ChatIDs {
		text := fmt.Sprintf("🔌 The bull-Tunnel panel is connected to the bot <b>@%s</b>.", Escape(username))
		if err := b.SendTo(chat, text, cfg); err != nil {
			failed = append(failed, fmt.Sprintf("chat %s: %v", chat, err))
		}
	}
	if len(failed) > 0 {
		return &Error{Message: strings.Join(failed, "; ")}
	}
	return nil
}

// Start begins the work of the bot: the sender and the poller.
func (b *Bot) Start() {
	b.Reload()
	go b.sender()
	go b.poller()
}

// Stop ends the work of the bot.
func (b *Bot) Stop() {
	b.once.Do(func() {
		close(b.stopped)
		b.stopBase()
	})
}

// Reload tells the bot that the settings may have changed. The bot reads
// them again at every turn of its work anyway; this makes it do so now: a
// long poll that is under way with the old token (or proxy, or address) is
// dropped, so the new settings are in use at once and not after the poll
// ends, and the name of the bot is asked once for the new token.
func (b *Bot) Reload() {
	cfg := b.GetConfig()
	key := keyOf(cfg) + fmt.Sprint(cfg.Enabled) + strings.Join(cfg.ChatIDs, ",")
	b.pollMu.Lock()
	changed := key != b.loaded
	b.loaded = key
	if changed && b.pollCancel != nil {
		b.pollCancel()
	}
	b.pollMu.Unlock()
	if !changed {
		return
	}
	// a chat that could not be written to may be the one that was fixed
	b.healthMu.Lock()
	b.current(cfg).sendErr = ""
	b.healthMu.Unlock()
	b.Status(cfg) // asks for the name of the bot when the token is new
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// pause waits, and ends early when the bot is stopped (it returns false) or
// when the settings change (the wait is over, the poller looks again).
func (b *Bot) pause(wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-b.stopped:
		return false
	case <-b.wake:
	case <-timer.C:
	}
	return true
}

// sender sends the messages of the queue.
func (b *Bot) sender() {
	for {
		select {
		case <-b.stopped:
			return
		case text := <-b.queue:
			cfg := b.GetConfig()
			for _, chat := range cfg.ChatIDs {
				for attempt := 0; attempt < 3; attempt++ {
					if err := b.SendTo(chat, text, &cfg); err == nil {
						break
					}
					if attempt == 2 {
						break
					}
					select {
					case <-b.stopped:
						return
					case <-time.After(b.backoff(attempt)):
					}
				}
			}
			// stay well under Telegram's rate limit
			select {
			case <-b.stopped:
				return
			case <-time.After(b.pace):
			}
		}
	}
}

// pollContext is the context of one long poll, which a change of the
// settings drops. It is made before the settings are read: a change that
// comes after it ends the poll, and one that came before it is read.
func (b *Bot) pollContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(b.base)
	b.pollMu.Lock()
	b.pollCancel = cancel
	b.pollMu.Unlock()
	return ctx, cancel
}

// poller asks Telegram for the messages of the chats. The settings are read
// again at every turn: a token that is changed, a bot that is switched on
// or off, or a proxy that is fixed is in use at the next turn, and a bot
// that started without a token waits for one instead of giving up.
func (b *Bot) poller() {
	token := "" // the bot the offset belongs to
	for {
		select {
		case <-b.stopped:
			return
		default:
		}
		ctx, cancel := b.pollContext()
		cfg := b.GetConfig()
		if !cfg.Enabled || cfg.BotToken == "" {
			cancel()
			if !b.pause(b.idle) {
				return
			}
			continue
		}
		if cfg.BotToken != token {
			// the numbers of the updates are the bot's own: the offset of
			// another bot would hide every message of this one
			token, b.offset = cfg.BotToken, 0
		}
		answer, err := b.callContext(ctx, "getUpdates", &cfg, map[string]any{"offset": b.offset, "timeout": 25})
		dropped := ctx.Err() != nil
		cancel()
		if dropped {
			continue // the settings changed or the bot stopped
		}
		if err != nil {
			if !b.pause(b.retry) {
				return
			}
			continue
		}
		b.ensureName(cfg)
		updates, _ := answer.([]any)
		for _, item := range updates {
			update, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := update["update_id"].(float64); ok {
				b.offset = int(id) + 1
			}
			message, _ := update["message"].(map[string]any)
			text := strings.TrimSpace(textOf(message["text"]))
			chat, _ := message["chat"].(map[string]any)
			chatID := idOf(chat["id"])
			if !strings.HasPrefix(text, "/") || chatID == "" {
				continue
			}
			b.handle(cfg, chatID, text)
		}
	}
}

// handle answers one command of one chat. A chat that is not one of the
// panel's gets one thing only: its own number, to /start — that is how the
// operator learns the Chat ID to enter, so it works before any is set. Every
// other command of such a chat is left unanswered.
func (b *Bot) handle(cfg model.Telegram, chatID string, text string) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return
	}
	command := strings.ToLower(strings.SplitN(words[0], "@", 2)[0])
	args := words[1:]
	if !contains(cfg.ChatIDs, chatID) {
		if command == "/start" {
			reply := fmt.Sprintf("This chat is not linked to the panel yet.\nChat ID: <code>%s</code>\n"+
				"Enter this number in Settings, Telegram bot, in the panel.", Escape(chatID))
			b.SendTo(chatID, reply, &cfg)
		}
		return
	}
	reply := "no handler"
	if b.Handler != nil {
		answer, err := b.Handler(command, args)
		if err != nil {
			reply = "error: " + Escape(err.Error())
		} else {
			reply = answer
		}
	}
	b.SendTo(chatID, reply, &cfg)
}

// idOf is the number of a chat as the operator writes it in the settings.
// The answer of Telegram is read into numbers of floating point, and a
// number like 123456789 printed as one comes out as 1.23456789e+08: the chat
// would never be the one of the settings.
func idOf(value any) string {
	switch id := value.(type) {
	case float64:
		return strconv.FormatInt(int64(id), 10)
	case string:
		return id
	}
	return ""
}

// contains tells whether a list holds a value.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// textOf is the text of a message.
func textOf(value any) string {
	text, _ := value.(string)
	return text
}

// Escape is the text as the HTML of Telegram wants it: the ampersand, the
// angle brackets and nothing else — the quotes are left alone.
func Escape(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(text)
}

// Truncate cuts a text of Telegram's HTML to at most limit characters.
//
// The cut is made by characters and not by bytes, which would split a letter
// of a language that takes more than one byte — and the HTML is kept valid:
// a tag or an entity the cut would leave half written is dropped, and the
// tags that are still open are closed. Telegram refuses a message it cannot
// parse, and the whole message would be lost for a few characters too many.
func Truncate(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	// room for the mark that says the text was cut, and for the tags that
	// have to be closed after it
	room := 1
	if limit > 4*closeRoom {
		room += closeRoom
	}
	out := string([]rune(text)[:max(limit-room, 0)])
	// an entity or a tag that was cut in the middle
	if i := strings.LastIndex(out, "<"); i >= 0 && !strings.Contains(out[i:], ">") {
		out = out[:i]
	}
	if i := strings.LastIndex(out, "&"); i >= 0 && !strings.Contains(out[i:], ";") && utf8.RuneCountInString(out[i:]) <= 10 {
		out = out[:i]
	}
	return out + "…" + closingTags(out)
}

// closeRoom is how many characters are kept free for the closing tags of a
// text that was cut.
const closeRoom = 40

// closingTags closes the tags a piece of HTML leaves open, innermost first.
func closingTags(text string) string {
	open := []string{}
	for {
		start := strings.IndexByte(text, '<')
		if start < 0 {
			break
		}
		end := strings.IndexByte(text[start:], '>')
		if end < 0 {
			break
		}
		tag := text[start+1 : start+end]
		text = text[start+end+1:]
		if tag == "" {
			continue
		}
		if tag[0] == '/' {
			name := strings.ToLower(strings.TrimSpace(tag[1:]))
			for i := len(open) - 1; i >= 0; i-- {
				if open[i] == name {
					open = open[:i]
					break
				}
			}
			continue
		}
		name := strings.ToLower(strings.Fields(tag)[0])
		if strings.HasSuffix(tag, "/") {
			continue
		}
		open = append(open, name)
	}
	out := ""
	for i := len(open) - 1; i >= 0; i-- {
		out += "</" + open[i] + ">"
	}
	return out
}
