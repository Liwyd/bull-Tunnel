package web

import (
	"fmt"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/friendly"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

/* What runs the tunnel engine on a server (the connection test, a test of the
   protocols, the change of a protocol, the command channel) can only be
   offered where the engine is, and where the server answers. When it cannot,
   the button is there but disabled, and the page says why and offers the one
   thing that puts it right — instead of letting the operator press it and read
   what the shell of the server says about a file that is not there. */

// engineBlock says what stands between the operator and an action that runs
// the engine on these servers: the sentences, and the buttons that put it
// right. Nothing in either means that the action can be run. self is the
// server whose drawer this is (it is called "this server"), or nil.
func engineBlock(self *model.Server, servers ...*model.Server) (notes []string, buttons []string) {
	for _, server := range servers {
		if server == nil {
			continue
		}
		name := "«" + escape(server.Name) + "»"
		if server == self {
			name = "this server"
		}
		switch {
		case server.Status == model.Offline:
			notes = append(notes, upperFirst(name)+" is not reachable over SSH.")
		case server.EngineVersion == "":
			notes = append(notes, "Install the tunnel engine on "+name+" first.")
			buttons = append(buttons, installButton(server, server == self))
		}
	}
	return notes, buttons
}

// installButton is the button that puts the engine on a server.
func installButton(server *model.Server, here bool) string {
	label := "Install the tunnel engine"
	if !here {
		label += " on «" + escape(server.Name) + "»"
	}
	return fmt.Sprintf(`<button type="button" class="btn sm" data-act="POST /api/servers/%s/install" data-done="Installation has started">%s</button>`,
		escape(server.ID), label)
}

// blockNote is the visible reason, and the buttons that answer it.
func blockNote(notes []string, buttons []string) string {
	if len(notes) == 0 {
		return ""
	}
	out := `<p class="note" data-engine-gate style="color:var(--degraded)">` + strings.Join(notes, " ") + `</p>`
	if len(buttons) > 0 {
		out += `<div class="btns">` + strings.Join(buttons, "") + `</div>`
	}
	return out
}

// off is the attribute of a button that cannot be pressed yet.
func off(blocked bool) string {
	if blocked {
		return ` disabled aria-disabled="true"`
	}
	return ""
}

// upperFirst puts a capital on the first letter of a sentence that begins
// with a word (and leaves one that begins with a quote mark alone).
func upperFirst(text string) string {
	if text == "" || text[0] < 'a' || text[0] > 'z' {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// serverSaid is the error of a server in the words of the operator (the panel
// keeps the failures it meets that way; one an older state kept in the words
// of the shell is said the same way here).
func serverSaid(s *model.Server) string {
	return friendly.Text(s.Error, friendly.Context{Server: s.Name, Port: s.SSHPort})
}

// linkSaid is the error of a tunnel in the words of the operator.
func (f *Facts) linkSaid(l *model.Link) string {
	return friendly.Text(l.Error, friendly.Context{Iran: f.Name[l.IranID], Kharej: f.Name[l.KharejID],
		TunnelPort: l.TunnelPort, Carrier: l.Transport})
}
