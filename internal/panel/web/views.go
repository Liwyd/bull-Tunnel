package web

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

/* The five views of the dashboard: the table of the network page, the page
   of the tunnels, the page of the domains, the page of the servers and the
   page of the settings. Each one is what a page holds, worked out by the
   panel from the state it has just read. */

// spark is the little chart of a tunnel's history.
func spark(history [][]float64, w, h int) string {
	points := history
	if len(points) > 40 {
		points = points[len(points)-40:]
	}
	if len(points) < 2 {
		return ""
	}
	max := 10.0
	for _, point := range points {
		if len(point) > 1 && point[1] > max {
			max = point[1]
		}
	}
	max *= 1.15
	step := float64(w) / float64(len(points)-1)
	path, fails := "", ""
	for i, point := range points {
		value := -1.0
		if len(point) > 1 {
			value = point[1]
		}
		x := float64(i) * step
		if value < 0 {
			fails += fmt.Sprintf(`<rect x="%.1f" y="0" width="2" height="%d" fill="var(--down)" opacity=".7"/>`, x-1, h)
			continue
		}
		y := float64(h) - (value/max)*(float64(h)-3) - 1.5
		prefix := "M"
		if i > 0 && len(points[i-1]) > 1 && points[i-1][1] >= 0 {
			prefix = "L"
		}
		path += fmt.Sprintf("%s%.1f,%.1f", prefix, x, y)
	}
	return fmt.Sprintf(`<svg width="%d" height="%d" viewBox="0 0 %d %d" style="display:block">%s`+
		`<path d="%s" fill="none" stroke="var(--muted)" stroke-width="1.4"/></svg>`, w, h, w, h, fails, path)
}

// dot is the little circle that says whether something is fine.
func dot(status string) string {
	return fmt.Sprintf(`<span class="dot %s"></span>`, escape(status))
}

// chip is a small label of the panel.
func chip(class, text string) string {
	return fmt.Sprintf(`<span class="chip %s">%s</span>`, class, text)
}

// transport is the carrier of a tunnel, as the table says it.
func (f *Facts) transport(link *model.Link) string {
	if f.status(link) == "testing" {
		return "…"
	}
	if link.Transport == "" {
		return "—"
	}
	return link.Transport
}

// latency is the measured time of a tunnel, or the word for its status.
func (f *Facts) latency(link *model.Link) string {
	if f.status(link) == "up" {
		return milliseconds(link.LatencyMS)
	}
	return fmt.Sprintf(`<span class="muted">%s</span>`, escape(say(f.status(link))))
}

// ports is what the Iran side of a tunnel opens.
func ports(link *model.Link) string {
	if len(link.Ports) == 0 {
		return "—"
	}
	return strings.Join(link.Ports, ", ")
}

// kind is who made the tunnel: the panel, or it was already there.
func kind(link *model.Link) string {
	if link.Managed {
		return chip("", "panel")
	}
	return chip("info", "existing")
}

// tags are the small labels on the path of a tunnel in the table.
func (f *Facts) tags(link *model.Link) string {
	out := []string{}
	if f.InUse[link.ID] {
		out = append(out, chip("ok", "● traffic"))
	}
	if carrier, there := f.State.Handover[link.ID]; there && carrier != "" {
		out = append(out, chip("purple", "ports on "+escape(f.Name[carrier])))
	}
	if f.flapping(link) {
		out = append(out, chip("warn", "unstable"))
	}
	return strings.Join(out, " ")
}

// networkTable is the table under the search of the network page.
func (f *Facts) networkTable(parts Parts, o Opts) {
	query := strings.ToLower(strings.TrimSpace(o.Query))
	rows := []*model.Link{}
	for _, link := range f.Links {
		status := f.status(link)
		if !o.Pending && status == "pending" {
			continue
		}
		if query != "" {
			text := strings.ToLower(f.label(link) + " " + link.Label + " " + link.Transport + " " + strings.Join(link.Ports, " "))
			if !strings.Contains(text, query) {
				continue
			}
		}
		if o.Filter != "" && o.Filter != "all" && status != o.Filter {
			continue
		}
		rows = append(rows, link)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if f.Name[rows[i].KharejID] != f.Name[rows[j].KharejID] {
			return f.Name[rows[i].KharejID] < f.Name[rows[j].KharejID]
		}
		if f.Name[rows[i].IranID] != f.Name[rows[j].IranID] {
			return f.Name[rows[i].IranID] < f.Name[rows[j].IranID]
		}
		return rows[i].Priority < rows[j].Priority
	})
	if len(rows) == 0 {
		note := "No tunnel matches this filter."
		if len(f.Links) == 0 {
			note = "No tunnel yet. Add the servers; the tunnels that already run on them are found automatically."
		}
		parts["links-table"] = fmt.Sprintf(`<tbody><tr><td class="muted" style="padding:20px 14px">%s</td></tr></tbody>`, note)
		return
	}
	body := strings.Builder{}
	body.WriteString(`<thead><tr><th></th><th>Path</th><th>Name</th><th>Protocol</th><th>Latency</th>` +
		`<th>Ports (Iran side)</th><th>Kind</th><th>History</th></tr></thead><tbody>`)
	for _, link := range rows {
		status := f.status(link)
		pin := ""
		if link.Pinned {
			pin = " 🔒"
		}
		body.WriteString(fmt.Sprintf(`<tr data-link="%s"><td>%s</td>
        <td class="pair">%s %s</td>
        <td class="muted">%s</td>
        <td class="mono">%s%s</td>
        <td class="mono">%s</td>
        <td class="mono">%s</td>
        <td>%s</td>
        <td>%s</td></tr>`,
			escape(link.ID), dot(status), escape(f.label(link)), f.tags(link),
			escape(link.Label), escape(f.transport(link)), pin,
			f.latency(link), escape(ports(link)), kind(link), spark(link.History, 90, 18)))
	}
	body.WriteString("</tbody>")
	parts["links-table"] = body.String()
}

// tunnels is the page of the tunnels: the table and the carriers.
func (f *Facts) tunnels(parts Parts, o Opts) {
	rows := make([]*model.Link, len(f.Links))
	copy(rows, f.Links)
	switch o.Sort {
	case "latency":
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := 1e9, 1e9
			if rows[i].LatencyMS != nil {
				a = *rows[i].LatencyMS
			}
			if rows[j].LatencyMS != nil {
				b = *rows[j].LatencyMS
			}
			return a < b
		})
	case "status":
		sort.SliceStable(rows, func(i, j int) bool {
			return f.status(rows[i]) < f.status(rows[j])
		})
	default:
		sort.SliceStable(rows, func(i, j int) bool { return f.label(rows[i]) < f.label(rows[j]) })
	}
	body := strings.Builder{}
	body.WriteString(`<thead><tr><th></th><th>Path</th><th>Protocol</th><th>Latency</th>` +
		`<th>Ports (Iran side)</th><th>Kind</th><th>Engine</th></tr></thead><tbody>`)
	for _, link := range rows {
		status := f.status(link)
		pin := ""
		if link.Pinned {
			pin = " 🔒"
		}
		body.WriteString(fmt.Sprintf(`<tr data-link="%s"><td>%s</td>
        <td class="pair">%s</td>
        <td class="mono">%s%s</td>
        <td class="mono">%s</td>
        <td class="mono">%s</td>
        <td>%s</td>
        <td class="mono muted">%s</td></tr>`,
			escape(link.ID), dot(status), escape(f.label(link)), escape(f.transport(link)), pin,
			f.latency(link), escape(ports(link)), kind(link), escape(f.engine(link))))
	}
	if len(rows) == 0 {
		body.WriteString(`<tr><td class="muted" style="padding:20px 14px">No tunnel yet.</td></tr>`)
	}
	body.WriteString("</tbody>")
	parts["tun-table"] = body.String()

	settings := f.State.Settings
	checked := map[string]bool{}
	for _, transport := range settings.CandidateTransports {
		checked[transport] = true
	}
	all := f.carriers()
	boxes := []string{}
	enabled := 0
	for _, transport := range all {
		if checked[transport] {
			enabled++
		}
		boxes = append(boxes, fmt.Sprintf(`<label><input type="checkbox" %s disabled>%s</label>`,
			boolAttr(checked[transport]), escape(transport)))
	}
	parts["tun-protocols"] = `<p class="note">The carriers the tunnel engine speaks. The ones that are ticked are tested on every check and used to repair a tunnel that breaks.</p>` +
		`<div class="tchecks">` + strings.Join(boxes, "") + `</div>` +
		`<dl class="kv" style="margin-top:6px">` +
		`<dt>Enabled</dt><dd class="mono">` + strconv.Itoa(enabled) + " / " + strconv.Itoa(len(all)) + `</dd>` +
		`<dt>Timeout</dt><dd class="mono">` + strconv.Itoa(settings.TestTimeout) + `s</dd>` +
		`<dt>Check interval</dt><dd class="mono">` + strconv.Itoa(settings.CheckInterval) + `s</dd>` +
		`<dt>Auto repair</dt><dd>` + onOff(settings.AutoRepair) + `</dd>` +
		`<dt>Mode</dt><dd>` + mode(settings.ObserveOnly) + `</dd>` +
		`</dl>` +
		`<div class="btns"><button class="btn sm" data-open-settings>` + iconEdit + ` Edit the protocols</button></div>`
}

// boolAttr is a checkbox, ticked or not.
func boolAttr(yes bool) string {
	if yes {
		return ` checked`
	}
	return ``
}

// onOff is a setting that is a switch.
func onOff(yes bool) string {
	if yes {
		return "on"
	}
	return "off"
}

// mode is how the panel works.
func mode(observeOnly bool) string {
	if observeOnly {
		return "observe only"
	}
	return "acting"
}

// engine is the version of the tunnel engine on the server the tunnel
// starts from: the tunnels page says which engine carries it.
func (f *Facts) engine(link *model.Link) string {
	if server := f.ByID[link.IranID]; server != nil && server.EngineVersion != "" {
		return server.EngineVersion
	}
	return "—"
}
