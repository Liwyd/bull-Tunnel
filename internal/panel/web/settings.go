package web

import (
	"fmt"
	"strings"
)

/* The page of the settings. The fields are the fields of the settings form
   in the modal: the same keys, the same marks — so the script collects what
   he wrote with one collector and saves it through the API already. This is
   the shape of the form, and the panel decides what is in it. */

// settings is the page of the settings: five sections, and the index of
// them on the left.
func (f *Facts) settings(parts Parts) {
	s := f.State.Settings
	tgOn := s.TG().Enabled

	section := func(number, title, kicker, body string) string {
		return fmt.Sprintf(`<div class="section" id="set-%s">
    <header><span class="n">%s</span><h3>%s</h3><span class="k">%s</span></header>
    <div class="modal-body">%s</div></div>`, number, number, title, kicker, body)
	}
	openSettings := `<div class="btns"><button class="btn sm" data-open-settings>` + iconEdit + ` Open the full settings</button></div>`

	// the carriers: the ones that are tested first, then the rest. The
	// group is marked the way the settings form marks it, so the collector
	// reads both the same way.
	checked := map[string]bool{}
	for _, transport := range s.CandidateTransports {
		checked[transport] = true
	}
	carriers := []string{}
	for _, transport := range f.carriers() {
		carriers = append(carriers, fmt.Sprintf(`<label><input type="checkbox" data-field="candidate_transports" data-checks value="%s"%s>%s</label>`,
			escape(transport), boolAttr(checked[transport]), escape(transport)))
	}

	accounts := []string{}
	for _, provider := range f.Providers {
		label := "ARVAN"
		if provider.Kind == "cloudflare" {
			label = "CF"
		}
		accounts = append(accounts, fmt.Sprintf(`<div class="opt"><span class="prov %s">%s</span>
        <span class="grow">%s <span class="mono muted small">%s</span></span>
        <button class="btn sm" data-verify-provider="%s">Test</button>
        <button class="btn sm" data-edit-provider="%s">%s Edit</button>
        <button class="btn sm danger" data-del-provider="%s">%s</button></div>`,
			escape(provider.Kind), label, escape(provider.Name), escape(provider.Zone),
			escape(provider.ID), escape(provider.ID), iconEdit, escape(provider.ID), iconTrash))
	}
	accountsBody := strings.Join(accounts, "")
	if accountsBody == "" {
		accountsBody = `<span class="note">No DNS account yet.</span>`
	}

	body := section("01", "Operating mode", "OBSERVE / MUTATE",
		checkConfirm("observe_only", s.ObserveOnly,
			`<b>Observe mode</b>: only check and report; no DNS record, config or service is changed automatically`,
			"Turn observe mode off? From now on the panel changes the DNS record itself when something fails.",
			"Turn observe mode off? From now on the panel changes the DNS record itself when something fails and rebuilds the broken tunnels with another protocol.",
			"auto_repair")+
			`<p class="hint">In this mode, everywhere the panel would have done something, it only says in the events and on Telegram what it would have done. What you do by hand in the panel is still carried out.</p>`+
			openSettings)

	body += section("02", "Monitor and recover", "MONITOR / RECOVER",
		`<div class="grid2">`+
			num("Check interval (seconds)", "check_interval", s.CheckInterval, s.CheckInterval, 10, 1)+
			num("Errors in a row that count as “down”", "fail_threshold", s.FailThreshold, s.FailThreshold, 1, 1)+
			`</div><div class="grid2">`+
			num("Fail back after (minutes)", "failback_after", s.FailbackAfter, s.FailbackAfter, 1, 60)+
			num("Timeout of each protocol in the test (seconds)", "test_timeout", s.TestTimeout, s.TestTimeout, 5, 1)+
			`</div><div class="grid2">`+
			num("“Unstable” = how many drops", "flap_count", s.FlapCount, s.FlapCount, 2, 1)+
			num("In how many minutes", "flap_window", s.FlapWindow, s.FlapWindow, 1, 60)+
			`</div>`+
			check("auto_repair", s.AutoRepair, "Rebuild the broken tunnels automatically with other protocols")+
			`<span class="label">Protocols that are tested</span>`+
			`<div class="tchecks">`+strings.Join(carriers, "")+`</div>`)

	body += section("03", "Discovery and engine", "DISCOVERY / ENGINE",
		check("auto_adopt", s.AutoAdopt, "Find and add the existing tunnels on the servers automatically")+
			check("auto_mesh", s.AutoMesh, "Suggest a tunnel for every Iran/foreign pair that has none")+
			`<div class="grid2">`+
			num("How often to look for tunnels (minutes)", "scan_interval", s.ScanInterval, s.ScanInterval, 1, 60)+
			text("Tunnel engine version", "engine_release", s.EngineRelease, "latest", ` class="mono" data-else="latest"`)+
			`</div>`+
			mono("Tunnel engine source URL", "engine_source", s.EngineSource, "https://github.com/Liwyd/bull-Tunnel/releases", "")+
			`<div class="grid2">`+
			num("First tunnel port", "tunnel_port_base", s.TunnelPortBase, s.TunnelPortBase, 0, 1)+
			num("First probe port (local)", "probe_port_base", s.ProbePortBase, s.ProbePortBase, 0, 1)+
			`</div>`+
			check("install_engine", s.InstallEngine, "Install the tunnel engine automatically on the servers that do not have it"))

	body += section("04", "DNS accounts", "PROVIDERS",
		`<div class="opt-list">`+accountsBody+`</div>`+
			`<div class="btns"><button class="btn sm" data-add="provider">`+iconAdd+` Add a DNS account</button></div>`)

	body += section("05", "Reporting and backups", "TELEGRAM / BACKUP",
		check("telegram.enabled", tgOn, "Send every event to Telegram")+
			check("backup_enabled", s.BackupEnabled, "Send a full backup to Telegram every night")+
			`<div class="grid2">`+
			mono("Backup time", "backup_time", s.BackupTime, "03:00", "")+
			mono("Backup time zone", "backup_tz", s.BackupTZ, "Asia/Tehran", ` data-else="Asia/Tehran"`)+
			`</div>`+openSettings)

	parts["set-body"] = body

	index := []struct{ number, title, text string }{
		{"01", "Operating mode", "Watch only, or act."},
		{"02", "Monitor and recover", "How often, and when to give up."},
		{"03", "Discovery and engine", "Find the tunnels, push the engine."},
		{"04", "DNS accounts", "ArvanCloud and Cloudflare."},
		{"05", "Reporting and backups", "Telegram and the nightly copy."},
	}
	buttons := []string{}
	for i, one := range index {
		class := ""
		if i == 0 {
			class = " on"
		}
		buttons = append(buttons, fmt.Sprintf(`<button data-goto="set-%s"%s><span class="n">%s</span><span class="t">%s</span><span class="d">%s</span></button>`,
			one.number, class, one.number, one.title, one.text))
	}
	parts["set-index"] = `<span class="label">Sections</span><div class="index">` + strings.Join(buttons, "") + `</div>`
}
