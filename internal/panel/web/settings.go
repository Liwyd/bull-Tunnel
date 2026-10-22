package web

import (
	"fmt"
	"strings"
)

/* The page of the settings. The form goes through the API already: every
   field is marked with the name it is saved under, and the script collects
   the fields and saves them — so this is the shape of the form, and the
   panel decides what is in it. */

// settings is the page of the settings: five sections, and the index of
// them on the left.
func (f *Facts) settings(parts Parts) {
	s := f.State.Settings
	tg := map[string]any{}
	if s.Telegram != nil {
		tg = s.Telegram
	}
	tgOn := false
	if value, ok := tg["enabled"].(bool); ok {
		tgOn = value
	}

	section := func(number, title, kicker, body string) string {
		return fmt.Sprintf(`<div class="section" id="set-%s">
    <header><span class="n">%s</span><h3>%s</h3><span class="k">%s</span></header>
    <div class="modal-body">%s</div></div>`, number, number, title, kicker, body)
	}
	checkBox := func(field, label string, on bool) string {
		return fmt.Sprintf(`<label class="check"><input type="checkbox" data-set="%s"%s> <span>%s</span></label>`,
			field, boolAttr(on), label)
	}
	number := func(field, label string, value, min int) string {
		return fmt.Sprintf(`<label>%s<input data-set="%s" type="number" min="%d" value="%d"></label>`, label, field, min, value)
	}
	minutes := func(field, label string, seconds, min int) string {
		return fmt.Sprintf(`<label>%s<input data-set="%s" type="number" min="%d" value="%d"></label>`, label, field, min, seconds/60)
	}
	openSettings := `<div class="btns"><button class="btn sm" data-open-settings>` + iconEdit + ` Open the full settings</button></div>`

	// the carriers: the ones that are tested first, then the rest
	checked := map[string]bool{}
	for _, transport := range s.CandidateTransports {
		checked[transport] = true
	}
	carriers := []string{}
	for _, transport := range f.carriers() {
		carriers = append(carriers, fmt.Sprintf(`<label><input type="checkbox" data-set-tr value="%s"%s>%s</label>`,
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
		checkBox("observe_only", `<b>Observe mode</b>: only check and report; no DNS record, config or service is changed automatically`, s.ObserveOnly)+
			`<p class="hint">In this mode, everywhere the panel would have done something, it only says in the events and on Telegram what it would have done. What you do by hand in the panel is still carried out.</p>`+
			openSettings)

	body += section("02", "Monitor and recover", "MONITOR / RECOVER",
		`<div class="grid2">`+
			number("check_interval", "Check interval (seconds)", s.CheckInterval, 10)+
			number("fail_threshold", "Errors in a row that count as “down”", s.FailThreshold, 1)+
			`</div><div class="grid2">`+
			minutes("failback_after", "Fail back after (minutes)", s.FailbackAfter, 1)+
			number("test_timeout", "Timeout of each protocol in the test (seconds)", s.TestTimeout, 5)+
			`</div><div class="grid2">`+
			number("flap_count", "“Unstable” = how many drops", s.FlapCount, 2)+
			minutes("flap_window", "In how many minutes", s.FlapWindow, 1)+
			`</div>`+
			checkBox("auto_repair", "Rebuild the broken tunnels automatically with other protocols", s.AutoRepair)+
			`<span class="label">Protocols that are tested</span>`+
			`<div class="tchecks">`+strings.Join(carriers, "")+`</div>`)

	body += section("03", "Discovery and engine", "DISCOVERY / ENGINE",
		checkBox("auto_adopt", "Find and add the existing tunnels on the servers automatically", s.AutoAdopt)+
			checkBox("auto_mesh", "Suggest a tunnel for every Iran/foreign pair that has none", s.AutoMesh)+
			`<div class="grid2">`+
			minutes("scan_interval", "How often to look for tunnels (minutes)", s.ScanInterval, 1)+
			`<label>Tunnel engine version<input data-set="engine_release" class="mono" value="`+escape(s.EngineRelease)+
			`" placeholder="latest"></label>`+
			`</div>`+
			`<label>Tunnel engine source URL<input data-set="engine_source" class="mono" value="`+escape(s.EngineSource)+
			`" placeholder="https://github.com/Liwyd/bull-Tunnel/releases"></label>`+
			`<div class="grid2">`+
			number("tunnel_port_base", "First tunnel port", s.TunnelPortBase, 0)+
			number("probe_port_base", "First probe port (local)", s.ProbePortBase, 0)+
			`</div>`+
			checkBox("install_engine", "Install the tunnel engine automatically on the servers that do not have it", s.InstallEngine))

	body += section("04", "DNS accounts", "PROVIDERS",
		`<div class="opt-list">`+accountsBody+`</div>`+
			`<div class="btns"><button class="btn sm" data-add="provider">`+iconAdd+` Add a DNS account</button></div>`)

	body += section("05", "Reporting and backups", "TELEGRAM / BACKUP",
		checkBox("tg_enabled", "Send every event to Telegram", tgOn)+
			checkBox("backup_enabled", "Send a full backup to Telegram every night", s.BackupEnabled)+
			`<div class="grid2">`+
			`<label>Backup time<input data-set="backup_time" class="mono" value="`+escape(s.BackupTime)+`" placeholder="03:00"></label>`+
			`<label>Backup time zone<input data-set="backup_tz" class="mono" value="`+escape(s.BackupTZ)+`" placeholder="Asia/Tehran"></label>`+
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
