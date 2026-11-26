package telegram

import (
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

/* What the bot knows about its own health. Every call to Telegram says how
   it went: the name of the bot (from getMe), or why the call failed. The
   settings page shows it, so the operator can see whether the token and the
   address are right without sending anything — and the page never waits for
   the network to be drawn: it reads what was written down here. */

// Status is what the bot knows about itself, without asking the network.
type Status struct {
	// Username is the name of the bot, from getMe; empty until it is known.
	Username string
	// Error is why the last call failed; empty when the last call of that
	// kind worked. The poll and the sending are told apart, so a poll that
	// works does not hide a chat the bot cannot write to.
	Error string
	// ErrorAt and OKAt are the moments of the last failure and of the last
	// call that worked, in seconds since 1970 (0 when there was none).
	ErrorAt float64
	OKAt    float64
	// Checking says that the name of the bot is being asked for.
	Checking bool
}

// health is the memory behind Status. It belongs to one bot of Telegram
// (token, address and proxy): another one starts it over.
type health struct {
	key      string
	username string
	asked    bool // getMe was asked for this bot
	checking bool

	pollErr, sendErr     string
	pollErrAt, sendErrAt time.Time
	okAt                 time.Time
}

// keyOf names the bot the settings point at. It holds the token: it is kept
// in the memory and compared, never shown.
func keyOf(cfg model.Telegram) string {
	return cfg.BotToken + "\n" + cfg.APIBase + "\n" + cfg.Proxy
}

// seconds is a moment as the pages count it.
func seconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}

// view is the health as Status says it. The caller holds healthMu.
func (h *health) view() Status {
	status := Status{Username: h.username, OKAt: seconds(h.okAt), Checking: h.checking}
	status.Error, status.ErrorAt = h.pollErr, seconds(h.pollErrAt)
	if h.sendErr != "" && (h.pollErr == "" || h.sendErrAt.After(h.pollErrAt)) {
		status.Error, status.ErrorAt = h.sendErr, seconds(h.sendErrAt)
	}
	return status
}

// current is the health of the bot the settings point at; a bot that is
// not the one it was kept for starts it over. The caller holds healthMu.
func (b *Bot) current(cfg model.Telegram) *health {
	key := keyOf(cfg)
	if b.health.key != key {
		b.health = health{key: key}
	}
	return &b.health
}

// SetOnStatus sets what is called when what Status says changes (the name of
// the bot became known, or an error appeared or went away), so the open
// pages can be told. It is called from another goroutine, with no lock held.
func (b *Bot) SetOnStatus(changed func()) {
	b.healthMu.Lock()
	b.onStatus = changed
	b.healthMu.Unlock()
}

// note writes down how a call went.
func (b *Bot) note(cfg model.Telegram, method string, result any, err error) {
	now := time.Now()
	b.healthMu.Lock()
	h := b.current(cfg)
	before := h.view()
	poll := method == "getUpdates" || method == "getMe"
	switch {
	case err != nil && poll:
		h.pollErr, h.pollErrAt = err.Error(), now
	case err != nil:
		h.sendErr, h.sendErrAt = err.Error(), now
	case poll:
		h.pollErr, h.okAt = "", now
	default:
		h.sendErr, h.okAt = "", now
	}
	if method == "getMe" && err == nil {
		if me, ok := result.(map[string]any); ok {
			if name, ok := me["username"].(string); ok {
				h.username = name
			}
		}
	}
	after := h.view()
	changed := b.onStatus
	b.healthMu.Unlock()
	if changed != nil && (before.Username != after.Username || before.Error != after.Error) {
		go changed()
	}
}

// Status is what the bot knows about itself, for the bot the settings point
// at. It never waits for the network: the name of the bot is asked for in
// the background, once for every token (and address, and proxy), and until
// the answer comes Checking is true.
func (b *Bot) Status(cfg model.Telegram) Status {
	b.healthMu.Lock()
	h := b.current(cfg)
	ask := cfg.BotToken != "" && !h.asked
	if ask {
		h.asked, h.checking = true, true
	}
	status := h.view()
	b.healthMu.Unlock()
	if ask {
		go b.identify(cfg)
	}
	return status
}

// identify asks Telegram who the bot is, and writes the answer down.
func (b *Bot) identify(cfg model.Telegram) {
	b.callContext(b.base, "getMe", &cfg, map[string]any{})
	b.healthMu.Lock()
	if b.health.key == keyOf(cfg) {
		b.health.checking = false
	}
	changed := b.onStatus
	b.healthMu.Unlock()
	if changed != nil {
		go changed()
	}
}

// ensureName asks for the name of the bot when it is not known yet, and
// nothing else is asking: a first answer that failed (the network was down)
// is tried again when a poll works.
func (b *Bot) ensureName(cfg model.Telegram) {
	b.healthMu.Lock()
	h := b.current(cfg)
	ask := h.username == "" && !h.checking
	if ask {
		h.asked, h.checking = true, true
	}
	b.healthMu.Unlock()
	if ask {
		b.identify(cfg)
	}
}
