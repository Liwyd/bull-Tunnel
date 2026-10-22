package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/backup"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

/* A form of the dashboard: what the fields hold, and where what the
   operator writes is sent. A form is the body of the API, drawn — every
   field carries the key the API names it by, and the form says which
   endpoint it is sent to, with which method, and what it says when it is
   done. The script collects what he wrote, field by field, and sends it:
   it does not know what a form is for. One truth, two shapes: a form is a
   write, drawn. */

// FormOpts is the form the operator asked for: which one, what it edits,
// what it starts from, and — for the restore form — the copies of the
// backup that are on this server.
type FormOpts struct {
	ID     string // server, link, provider, domain, settings, restore
	Edit   string // the id of what is being edited, if anything
	Role   string // kharej or iran, when a server is added
	Preset string // a server to preselect on its side in the tunnel form
	Copies []backup.File
}

// FormAnswer is one form of the dashboard: what it holds, and where what
// the operator writes is sent. The script sets the last three on the form
// it puts the markup in.
type FormAnswer struct {
	HTML     string `json:"html"`
	Method   string `json:"method"`
	Endpoint string `json:"endpoint"`
	Done     string `json:"done"`
	After    string `json:"after,omitempty"`  // what to do once it is saved
	Reload   bool   `json:"reload,omitempty"` // the page is loaded again
}

// secretMask is what a secret the panel holds is shown as. The API keeps
// the old value when it is given the mask back, so a secret the operator
// does not touch is a secret that does not change.
const secretMask = "••••"

// The names of the carriers, as the panel shows them.
var carrierNames = map[string]string{
	"tcpmux": "TCP Mux", "wsmux": "WS Mux", "tcp": "TCP", "ws": "WebSocket",
	"stealth": "Stealth", "kcp": "KCP", "quic": "QUIC", "wssmux": "WSS Mux",
	"wss": "WSS", "udp": "UDP",
}

// Form is one form of the dashboard. It answers false when the panel has
// no such form.
func (f *Facts) Form(o FormOpts) (FormAnswer, bool) {
	switch o.ID {
	case "server":
		return f.serverForm(o), true
	case "link":
		return f.linkForm(o), true
	case "provider":
		return f.providerForm(o), true
	case "domain":
		return f.domainForm(o), true
	case "settings":
		return f.settingsForm(), true
	case "restore":
		return f.restoreForm(o.Copies), true
	}
	return FormAnswer{}, false
}

/* The lines of a form. Every field carries the key the API names it by,
   so what the operator wrote is collected without the script knowing what
   the form is for. */

// one is a line: a label and what he writes in it.
func one(label, body string) string { return `<label>` + label + body + `</label>` }

// text is a field he types into. attrs is what the field also carries: a
// class, a value to fall back on when he leaves it empty.
func text(label, key, value, placeholder, attrs string) string {
	return one(escape(label), fmt.Sprintf(`<input data-field="%s" value="%s" placeholder="%s"%s>`,
		escape(key), escape(value), escape(placeholder), attrs))
}

// mono is a field of addresses, ports and keys.
func mono(label, key, value, placeholder, attrs string) string {
	return text(label, key, value, placeholder, ` class="mono"`+attrs)
}

// num is a field of numbers. scale is what turns the seconds the panel
// holds into the minutes the operator reads (and turns them back).
func num(label, key string, value, otherwise, min, scale int) string {
	shown := value
	if scale > 1 {
		shown = (value + scale/2) / scale
	}
	return one(escape(label), fmt.Sprintf(
		`<input data-field="%s" data-num data-def="%d" data-scale="%d" type="number" min="%d" value="%d">`,
		escape(key), otherwise, scale, min, shown))
}

// check is a switch, with the words he reads next to it.
func check(key string, on bool, label string) string {
	return checkRich(key, on, escape(label))
}

// checkRich is a switch whose words hold markup of their own.
func checkRich(key string, on bool, labelHTML string) string {
	return fmt.Sprintf(`<label class="check"><input data-field="%s" type="checkbox"%s> <span>%s</span></label>`,
		escape(key), boolAttr(on), labelHTML)
}

// checkConfirm is a switch that asks him before it is turned off. The
// second question is asked when the switch named by when is on.
func checkConfirm(key string, on bool, labelHTML, confirm, confirm2, when string) string {
	return fmt.Sprintf(`<label class="check"><input data-field="%s" type="checkbox"%s`+
		` data-confirm="%s" data-confirm-2="%s" data-confirm-2-when="%s"> <span>%s</span></label>`,
		escape(key), boolAttr(on), escape(confirm), escape(confirm2), escape(when), labelHTML)
}

// checkEnd is a switch that lines up with the field beside it.
func checkEnd(key string, on bool, label string) string {
	return fmt.Sprintf(`<label class="check" style="align-self:end"><input data-field="%s" type="checkbox"%s> %s</label>`,
		escape(key), boolAttr(on), escape(label))
}

// pick is a field of choices. The choices are the state's, in the order
// the panel keeps them.
func pick(label, key, value string, options [][2]string) string {
	rows := strings.Builder{}
	for _, option := range options {
		rows.WriteString(fmt.Sprintf(`<option value="%s"%s>%s</option>`,
			escape(option[0]), boolWhen(option[0] == value), escape(option[1])))
	}
	return one(escape(label), fmt.Sprintf(`<select data-field="%s">%s</select>`, escape(key), rows.String()))
}

// secret is a password: the panel shows the mask of the one it holds, and
// the API keeps it when the mask is sent back.
func secret(label, key, value, placeholder string) string {
	shown := ""
	if value != "" {
		shown = secretMask
	}
	return one(escape(label), fmt.Sprintf(
		`<input data-field="%s" type="password" autocomplete="off" value="%s" placeholder="%s">`,
		escape(key), escape(shown), escape(placeholder)))
}

// area is a field of several lines: ports, chat ids.
func area(label, key, value, placeholder string) string {
	return one(escape(label), fmt.Sprintf(`<textarea data-field="%s" data-lines placeholder="%s">%s</textarea>`,
		escape(key), escape(placeholder), escape(value)))
}

// hidden is a field he does not see: what a form keeps when he changes
// something else. The API writes the whole thing from the body, so what
// the form does not send is lost.
func hidden(key, value string) string {
	return fmt.Sprintf(`<input type="hidden" data-field="%s" value="%s">`, escape(key), escape(value))
}

// hiddenLines is a hidden field of several lines: the group of a domain.
func hiddenLines(key string, values []string) string {
	return fmt.Sprintf(`<input type="hidden" data-field="%s" data-lines value="%s">`,
		escape(key), escape(strings.Join(values, ", ")))
}

// hint is a line that explains what is above it; it holds markup.
func hint(text string) string { return `<p class="hint">` + text + `</p>` }

// why is a line that says why this form is on the screen instead of the
// one he asked for.
func why(text string) string { return `<p class="note">` + escape(text) + `</p>` }

// formActions is the bottom of a form.
func formActions(label string) string {
	return `<div class="modal-actions"><button type="button" class="btn ghost" data-close-modal>Cancel</button>` +
		`<button class="btn primary" type="submit">` + escape(label) + `</button></div>`
}

// boolWhen is the attribute of the choice that is picked.
func boolWhen(yes bool) string {
	if yes {
		return ` selected`
	}
	return ``
}

/* The forms themselves. */

// serverForm adds a server, or edits one.
func (f *Facts) serverForm(o FormOpts) FormAnswer {
	var server *model.Server
	if o.Edit != "" {
		server = f.ByID[o.Edit]
	}
	role := o.Role
	if server != nil {
		role = server.Role
	}
	if role != "iran" {
		role = "kharej"
	}
	kharej := role == "kharej"
	edit := server != nil

	answer := FormAnswer{Method: "POST", Endpoint: "/api/servers",
		Done: "Server added; connecting and looking for tunnels…"}
	if edit {
		answer.Method, answer.Endpoint = "PUT", "/api/servers/"+server.ID
		answer.Done = "Saved"
	}

	name, ip, user := "", "", "root"
	port, priority := 22, 100
	enabled, probe, hostKey := true, "", ""
	password, key := "", ""
	if server != nil {
		name, ip, user = server.Name, server.IP, server.SSHUser
		port, priority, enabled = server.SSHPort, server.Priority, server.Enabled
		probe, hostKey = server.ProbeTarget, server.HostKey
		// the secrets are shown as the mask of them: the panel keeps the
		// one it holds when the mask is sent back untouched.
		if server.SSHPassword != "" {
			password = secretMask
		}
		if server.SSHKey != "" {
			key = secretMask
		}
	}
	where, placeholder := "foreign", "Hetzner-DE"
	if !kharej {
		where, placeholder = "Iran", "Tehran-1"
	}
	passwordPlaceholder := ""
	if edit {
		passwordPlaceholder = "empty = unchanged"
	}

	body := strings.Builder{}
	body.WriteString(`<h2>` + map[bool]string{true: "Edit ", false: "Add "}[edit] + where + ` server</h2>`)
	// the role is not on the form, but the API needs it to know what kind
	// of server this is.
	body.WriteString(hidden("role", role))
	if edit {
		// the command key is not on the form either, and the API writes
		// the whole server from the body: what is not sent is lost.
		body.WriteString(hidden("command_key", server.CommandKey))
	}
	body.WriteString(`<div class="grid2">` +
		text("Name", "name", name, placeholder, " required") +
		mono("IP address", "ip", ip, "1.2.3.4", " required") + `</div>`)
	body.WriteString(`<div class="grid2">` +
		text("SSH user", "ssh_user", user, "", ` data-else="root"`) +
		num("SSH port", "ssh_port", port, 22, 1, 1) + `</div>`)
	body.WriteString(secret("SSH password", "ssh_password", password, passwordPlaceholder) +
		one("SSH private key (optional)", fmt.Sprintf(
			`<textarea data-field="ssh_key" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----">%s</textarea>`, escape(key))) +
		mono("Host key fingerprint", "host_key", hostKey, "SHA256:...", ""))
	body.WriteString(hint(`Leave it empty and the panel pins the key it saw the first time. If you copy the fingerprint from the server itself (<span class="mono">ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub</span>), only that key is accepted.`))
	if kharej {
		body.WriteString(mono("Health probe target", "probe_target", probe, "127.0.0.1:"+strconv.Itoa(port), ""))
		body.WriteString(hint("To test the real path, the panel connects to this address from inside the tunnel (default: the SSH of this server)."))
	} else {
		body.WriteString(hidden("probe_target", ""))
	}
	body.WriteString(`<div class="grid2">` +
		num("Priority (lower comes first)", "priority", priority, 100, 0, 1) +
		checkEnd("enabled", enabled, "Enabled") + `</div>`)
	if kharej {
		body.WriteString(hint("When a foreign server fails completely, its ports move to a foreign server with a lower number. If they are all equal, the order is the alphabetical order of the names. The “primary” server of a domain is a separate thing and does not follow the priority."))
	} else {
		body.WriteString(hint("On a failure, the domain moves to an Iran server with a lower number. If they are all equal, the order is the alphabetical order of the names. The “primary” server of a domain is a separate thing and does not follow the priority."))
	}
	if !edit {
		body.WriteString(hint("Once it is connected, the panel finds and adds the tunnels that already run on the servers by itself."))
	}
	body.WriteString(formActions(map[bool]string{true: "Save", false: "Add"}[edit]))
	answer.HTML = body.String()
	return answer
}

// linkForm builds a tunnel between two servers.
func (f *Facts) linkForm(o FormOpts) FormAnswer {
	if len(f.Kharej) == 0 || len(f.Iran) == 0 {
		// the servers come first: the form of the one that is missing is
		// opened, with a line that says why.
		role := "kharej"
		if len(f.Kharej) > 0 {
			role = "iran"
		}
		answer := f.serverForm(FormOpts{Role: role})
		answer.HTML = why("Add the Iran and the foreign servers first; the tunnel is added right after.") + answer.HTML
		return answer
	}

	answer := FormAnswer{Method: "POST", Endpoint: "/api/links", Done: "The tunnel is being built"}
	iran, kharej := o.Preset, o.Preset
	if o.Preset != "" {
		if server := f.ByID[o.Preset]; server != nil && server.Role == "iran" {
			iran, kharej = o.Preset, ""
		} else {
			iran, kharej = "", o.Preset
		}
	}
	options := func(list []*model.Server) [][2]string {
		out := [][2]string{}
		for _, server := range list {
			out = append(out, [2]string{server.ID, server.Name + " — " + server.IP})
		}
		return out
	}
	protocols := [][2]string{{"", "Automatic (test them all and pick the best)"}}
	for _, transport := range f.Transports {
		name := transport
		if there, ok := carrierNames[transport]; ok {
			name = there
		}
		protocols = append(protocols, [2]string{transport, name})
	}

	body := strings.Builder{}
	body.WriteString(`<h2>New tunnel</h2>`)
	body.WriteString(`<div class="grid2">` +
		pick("Foreign server", "kharej_id", kharej, options(f.Kharej)) +
		pick("Iran server", "iran_id", iran, options(f.Iran)) + `</div>`)
	body.WriteString(area("Forwarded ports (Iran side)", "ports", "", "443\n8443=2096"))
	body.WriteString(`<div class="grid2">` +
		pick("Protocol", "transport", "", protocols) +
		text("Display name", "label", "", "e.g. spare-ws", "") + `</div>`)
	body.WriteString(hint("Between one pair of servers you can have several tunnels with different protocols, and put them in the group of a domain."))
	body.WriteString(formActions("Create"))
	answer.HTML = body.String()
	return answer
}

// providerForm adds a DNS account, or edits one.
func (f *Facts) providerForm(o FormOpts) FormAnswer {
	var account *model.DNSProvider
	if o.Edit != "" {
		account = f.State.Providers.Get(o.Edit)
	}
	edit := account != nil

	answer := FormAnswer{Method: "POST", Endpoint: "/api/providers", Done: "Saved"}
	if edit {
		answer.Method, answer.Endpoint = "PUT", "/api/providers/"+account.ID
	}

	name, kind, zone, proxied, token := "", "arvan", "", false, ""
	if account != nil {
		name, kind, zone, proxied = account.Name, account.Kind, account.Zone, account.Proxied
		if account.APIToken != "" {
			token = secretMask
		}
	}
	tokenPlaceholder := ""
	if edit {
		tokenPlaceholder = "empty = unchanged"
	}

	body := strings.Builder{}
	body.WriteString(`<h2>` + map[bool]string{true: "Edit ", false: "Add "}[edit] + `DNS account</h2>`)
	body.WriteString(`<div class="grid2">` +
		text("Name", "name", name, "main Arvan", " required") +
		pick("Provider", "kind", kind, [][2]string{{"arvan", "ArvanCloud"}, {"cloudflare", "Cloudflare"}}) + `</div>`)
	body.WriteString(mono("Zone (the root domain)", "zone", zone, "example.ir", " required"))
	body.WriteString(secret("API Key / Token", "api_token", token, tokenPlaceholder))
	body.WriteString(hint("Arvan: the API key (with or without the Apikey prefix). Cloudflare: an API Token with the Zone → DNS → Edit permission."))
	body.WriteString(check("proxied", proxied,
		"The records are proxied (ArvanCloud / Cloudflare CDN) — usually off for a tunnel"))
	body.WriteString(formActions(map[bool]string{true: "Save", false: "Add"}[edit]))
	answer.HTML = body.String()
	return answer
}

// domainForm adds a domain, or edits one.
func (f *Facts) domainForm(o FormOpts) FormAnswer {
	if len(f.Providers) == 0 {
		// a domain needs a DNS account before anything else: the form of
		// the account is the one that comes, with a line that says why.
		return withWhy(f.providerForm(FormOpts{}), "Add a DNS account first; the domain is added right after.")
	}
	if len(f.Kharej) == 0 {
		// and, if the panel is to point it somewhere, a foreign server.
		return withWhy(f.serverForm(FormOpts{Role: "kharej"}), "Add the servers first; the domain is added right after.")
	}

	var domain *model.Domain
	if o.Edit != "" {
		for _, candidate := range f.Domains {
			if candidate.ID == o.Edit {
				domain = candidate
			}
		}
	}
	edit := domain != nil

	answer := FormAnswer{Method: "POST", Endpoint: "/api/domains", Done: "Saved", After: "domain"}
	if edit {
		answer.Method, answer.Endpoint, answer.After = "PUT", "/api/domains/"+domain.ID, ""
	}

	fqdn, kharej, ports, ttl, autoFailover := "", "", "", 120, true
	providerID, group := f.Providers[0].ID, []string{}
	if edit {
		fqdn, kharej, ttl, autoFailover = domain.FQDN, domain.KharejID, domain.TTL, domain.AutoFailover
		providerID, group = domain.ProviderID, domain.Group
		ports = strings.Join(domain.Ports, ", ")
	}

	accounts := [][2]string{}
	for _, account := range f.Providers {
		accounts = append(accounts, [2]string{account.ID, account.Name + " (" + account.Zone + ")"})
	}
	offices := [][2]string{{"", "— I will pick the ports myself"}}
	for _, server := range f.Kharej {
		offices = append(offices, [2]string{server.ID, server.Name})
	}

	body := strings.Builder{}
	body.WriteString(`<h2>` + map[bool]string{true: "Edit ", false: "Add "}[edit] + `domain</h2>`)
	if edit {
		// the servers the record is on are not on the form, but the API
		// writes the whole domain from the body: what is not sent is lost.
		body.WriteString(hidden("iran_id", domain.IranID) + hidden("primary_iran", domain.PrimaryIran) +
			hiddenLines("group", group))
	}
	body.WriteString(mono("Domain", "fqdn", fqdn, "sub.example.ir", " required"))
	body.WriteString(`<div class="grid2">` +
		pick("DNS account", "provider_id", providerID, accounts) +
		pick("Foreign server (optional)", "kharej_id", kharej, offices) + `</div>`)
	body.WriteString(mono("The ports of this domain (optional)", "ports", ports, "443, 2053", ""))
	body.WriteString(hint("The ports the users of this domain connect to. Every port can go to a different foreign server. Leave it empty and, once the record has been read, tick the ports in the domain panel from the tunnels of that Iran server (or, if you picked a foreign server, the ports of the tunnels of that foreign server are used)."))
	body.WriteString(`<div class="grid2">` +
		num("TTL (seconds)", "ttl", ttl, 120, 60, 1) +
		checkEnd("auto_failover", autoFailover, "Automatic failover on a failure") + `</div>`)
	body.WriteString(formActions(map[bool]string{true: "Save", false: "Add"}[edit]))
	answer.HTML = body.String()
	return answer
}

// withWhy puts a line above a form that is standing in for another one.
func withWhy(answer FormAnswer, text string) FormAnswer {
	answer.HTML = why(text) + answer.HTML
	return answer
}

// settingsForm is the whole settings of the panel.
func (f *Facts) settingsForm() FormAnswer {
	s := f.State.Settings
	tg := s.TG()

	body := strings.Builder{}
	body.WriteString(`<h2>Settings</h2>`)

	body.WriteString(`<fieldset><legend>Working mode</legend>`)
	body.WriteString(checkConfirm("observe_only", s.ObserveOnly,
		`<b>Observe mode</b>: only check and report; no DNS record, config or service is changed automatically`,
		"Turn observe mode off? From now on the panel changes the DNS record itself when something fails.",
		"Turn observe mode off? From now on the panel changes the DNS record itself when something fails and rebuilds the broken tunnels with another protocol.",
		"auto_repair"))
	body.WriteString(hint("In this mode, everywhere the panel would have done something, it only says in the events and on Telegram what it would have done. What you do by hand in the panel is still carried out."))
	body.WriteString(`</fieldset>`)

	body.WriteString(`<fieldset><legend>Monitoring and failover</legend>`)
	body.WriteString(`<div class="grid2">` +
		num("Check interval (seconds)", "check_interval", s.CheckInterval, s.CheckInterval, 10, 1) +
		num("How many errors in a row count as “down”", "fail_threshold", s.FailThreshold, s.FailThreshold, 1, 1) + `</div>`)
	body.WriteString(`<div class="grid2">` +
		num("Fail back after how many minutes of stability", "failback_after", s.FailbackAfter, s.FailbackAfter, 1, 60) +
		num("Timeout of each protocol in the test (seconds)", "test_timeout", s.TestTimeout, s.TestTimeout, 5, 1) + `</div>`)
	body.WriteString(`<div class="grid2">` +
		num("“Unstable” = how many drops", "flap_count", s.FlapCount, s.FlapCount, 2, 1) +
		num("In how many minutes", "flap_window", s.FlapWindow, s.FlapWindow, 1, 60) + `</div>`)
	body.WriteString(check("auto_repair", s.AutoRepair, "Rebuild the broken tunnels automatically with other protocols"))
	body.WriteString(`<label>The protocols that are tested</label><div class="tchecks">`)
	checked := map[string]bool{}
	for _, transport := range s.CandidateTransports {
		checked[transport] = true
	}
	for _, transport := range f.carriers() {
		body.WriteString(fmt.Sprintf(`<label><input type="checkbox" data-field="candidate_transports" data-checks value="%s"%s>%s</label>`,
			escape(transport), boolAttr(checked[transport]), escape(transport)))
	}
	body.WriteString(`</div></fieldset>`)

	body.WriteString(`<fieldset><legend>Servers and tunnel engine</legend>`)
	body.WriteString(check("auto_adopt", s.AutoAdopt, "Find and add the existing tunnels on the servers automatically"))
	body.WriteString(check("auto_mesh", s.AutoMesh, "Suggest a tunnel for every Iran/foreign pair that has none"))
	body.WriteString(`<div class="grid2">` +
		num("How often to look for tunnels (minutes)", "scan_interval", s.ScanInterval, s.ScanInterval, 1, 60) +
		text("Tunnel engine version", "engine_release", s.EngineRelease, "latest", ` class="mono" data-else="latest"`) + `</div>`)
	body.WriteString(mono("Tunnel engine source URL", "engine_source", s.EngineSource, "https://github.com/Liwyd/bull-Tunnel/releases", ""))
	body.WriteString(secret("Source token (for a private repository)", "engine_token", s.EngineToken, "empty = no token"))
	body.WriteString(`<div class="grid2">` +
		num("First tunnel port", "tunnel_port_base", s.TunnelPortBase, s.TunnelPortBase, 0, 1) +
		num("First probe port (local)", "probe_port_base", s.ProbePortBase, s.ProbePortBase, 0, 1) + `</div>`)
	body.WriteString(check("install_engine", s.InstallEngine, "Install the tunnel engine automatically on the servers that do not have it"))
	body.WriteString(`</fieldset>`)

	body.WriteString(`<fieldset><legend>Command channel (DNS)</legend>`)
	body.WriteString(check("command_enabled", s.CommandEnabled, "The command channel is on"))
	accounts := [][2]string{{"", "— not selected —"}}
	for _, account := range f.Providers {
		accounts = append(accounts, [2]string{account.ID, account.Name + " (" + account.Zone + ")"})
	}
	body.WriteString(pick("DNS account of the command channel", "command_provider_id", s.CommandProviderID, accounts))
	body.WriteString(hint(`When not even TCP is possible, the panel writes the command into a TXT record and the “agent” on the server reads it over DNS and runs it. For security, create a separate zone with a separate account: a server that has failed must not be able to touch the records of the main domains.`))
	body.WriteString(`</fieldset>`)

	body.WriteString(`<fieldset><legend>Telegram bot</legend>`)
	body.WriteString(check("telegram.enabled", tg.Enabled, "Send every event to Telegram"))
	body.WriteString(secret("Bot token", "telegram.bot_token", tg.BotToken, "123456:ABC..."))
	body.WriteString(mono("Chat IDs (comma separated)", "telegram.chat_ids", strings.Join(tg.ChatIDs, ", "),
		"123456789, -100987654321", " data-lines"))
	body.WriteString(hint(`Do not know the Chat ID? Send <span class="mono">/start</span> to the bot.`))
	body.WriteString(`<div class="grid2">` +
		mono("API URL (reverse proxy)", "telegram.api_base", tg.APIBase, "",
			` data-else="https://api.telegram.org"`) +
		mono("Proxy", "telegram.proxy", tg.Proxy, "socks5://127.0.0.1:1080", "") + `</div>`)
	body.WriteString(`<div class="btns"><button type="button" class="btn sm" data-act="POST /api/telegram/test" data-act-first data-done="The test message was sent">Send a test message</button></div>`)
	body.WriteString(`</fieldset>`)

	body.WriteString(`<fieldset><legend>Backups</legend>`)
	body.WriteString(check("backup_enabled", s.BackupEnabled, "Send a full backup to Telegram every night"))
	body.WriteString(`<div class="grid2">` +
		mono("Time", "backup_time", s.BackupTime, "03:00", "") +
		mono("Time zone", "backup_tz", s.BackupTZ, "Asia/Tehran", ` data-else="Asia/Tehran"`) + `</div>`)
	body.WriteString(`<div class="grid2">` +
		secret("Backup password", "backup_password", s.BackupPassword, "empty = no password") +
		num("How many copies to keep on this server", "backup_keep", s.BackupKeep, 14, 1, 1) + `</div>`)
	body.WriteString(hint("A backup holds every server, the SSH passwords, the tokens, the domains, the settings and the panel login; always set a password and keep a copy of it somewhere else. Without that password the backup cannot be opened."))
	body.WriteString(`<div class="btns">` +
		`<button type="button" class="btn sm" data-act="POST /api/backups/run" data-act-error="error" data-done="The backup was sent to Telegram">Send a backup now</button>` +
		`<a class="btn sm" href="/api/backups/download" download>Download the backup</a>` +
		`<button type="button" class="btn sm" data-act-form="restore">Restore a backup…</button></div>`)
	body.WriteString(`</fieldset>`)

	body.WriteString(`<fieldset><legend>DNS accounts</legend><div class="opt-list">`)
	for _, account := range f.Providers {
		label := "ARVAN"
		if account.Kind == "cloudflare" {
			label = "CF"
		}
		body.WriteString(fmt.Sprintf(`<div class="opt"><span class="prov %s">%s</span><span class="grow">%s <span class="mono muted small">%s</span></span>
        <button type="button" class="btn sm" data-verify-provider="%s">Test</button><button type="button" class="btn sm" data-edit-provider="%s">Edit</button><button type="button" class="btn sm danger" data-del-provider="%s">Delete</button></div>`,
			escape(account.Kind), label, escape(account.Name), escape(account.Zone),
			escape(account.ID), escape(account.ID), escape(account.ID)))
	}
	if len(f.Providers) == 0 {
		body.WriteString(`<span class="muted small">—</span>`)
	}
	body.WriteString(`</div><div class="btns"><button type="button" class="btn sm" data-add="provider">` + iconAdd + ` DNS account</button></div></fieldset>`)

	body.WriteString(formActions("Save"))
	return FormAnswer{Method: "PUT", Endpoint: "/api/settings", Done: "Settings saved", HTML: body.String()}
}

// restoreForm puts back a backup.
func (f *Facts) restoreForm(copies []backup.File) FormAnswer {
	body := strings.Builder{}
	body.WriteString(`<h2>Restore a backup</h2>`)
	body.WriteString(`<p class="note">Every server, tunnel, domain, DNS account and panel setting is replaced by the content of the backup. Nothing is changed <b>on the servers themselves or in the DNS records</b>; after the restore the panel checks everything again. The current state is backed up separately first.</p>`)
	body.WriteString(one("Backup file (from Telegram or downloaded)",
		`<input type="file" data-file accept=".tmbk,.gz,.json">`))
	if len(copies) > 0 {
		body.WriteString(`<label>Or one of the copies on this server</label><div class="opt-list">`)
		for _, copy := range copies {
			download := "/api/backups/download?name=" + escape(copy.Name)
			lock := ""
			if copy.Encrypted {
				lock = " · 🔒"
			}
			body.WriteString(fmt.Sprintf(`<label class="opt"><input type="radio" data-field="bk_name" value="%s">
      <span class="grow"><span class="mono">%s</span><span class="sub">%s · %s%s</span></span>
      <a class="btn sm ghost" href="%s" download>Download</a></label>`,
				escape(copy.Name), escape(copy.Name), escape(clock(copy.Mtime)), escape(size(copy.Size)), lock, download))
		}
		body.WriteString(`</div>`)
	}
	body.WriteString(secret("Backup password", "password", "", "empty = the password from the settings"))
	body.WriteString(check("observe", true, "Stay in observe mode after the restore (recommended)"))
	body.WriteString(check("restore_login", true, "Restore the panel username and password as well"))
	body.WriteString(formActions("Restore"))
	return FormAnswer{Method: "POST", Endpoint: "/api/backups/restore",
		Done: "Restored: {summary}", Reload: true, HTML: body.String()}
}

// clock is a time, as a file is dated.
func clock(ts float64) string {
	return time.Unix(int64(ts), 0).Format("2 Jan 15:04")
}

// size is the size of a file, as the panel says it.
func size(bytes int64) string {
	if bytes > 1048576 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/1048576)
	}
	return fmt.Sprintf("%d KB", (bytes+1023)/1024)
}
