package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cmdStatus is the state of the panel in one screen.
func cmdStatus(panel *Panel, args []string) (int, error) {
	st, err := panel.state()
	if err != nil {
		return 1, err
	}
	servers := listOf(st, "servers")
	links := listOf(st, "links")
	domains := listOf(st, "domains")
	on, up, degraded, down, ok := 0, 0, 0, 0, 0
	for _, server := range servers {
		if textOf(server, "status") == "online" {
			on++
		}
	}
	for _, link := range links {
		switch textOf(link, "status") {
		case "up":
			up++
		case "degraded":
			degraded++
		case "down":
			down++
		}
	}
	for _, domain := range domains {
		if textOf(domain, "status") == "ok" {
			ok++
		}
	}

	out.Title(fmt.Sprintf("bull-Tunnel %s", orText(st, "version", version)))
	out.Field("panel", panel.Base)
	out.Field("servers", fmt.Sprintf("%d of %d online", on, len(servers)))
	out.Field("tunnels", fmt.Sprintf("%d up, %d unstable, %d down, %d not built", up, degraded, down, len(links)-up-degraded-down))
	out.Field("domains", fmt.Sprintf("%d of %d healthy", ok, len(domains)))
	if last, there := st["last_check"].(float64); there && last > 0 {
		out.Field("last check", time.Unix(int64(last), 0).Format("2006-01-02 15:04:05"))
	}
	mode := "acting"
	if settings, there := st["settings"].(map[string]any); there {
		if observe, there := settings["observe_only"].(bool); there && observe {
			mode = "observe only"
		}
	}
	out.Field("mode", mode)
	for _, e := range listOf(st, "errors") {
		out.Warn(textOfAny(e))
	}

	if len(servers) > 0 {
		out.Title("Servers")
		rows := [][]string{}
		for _, server := range servers {
			role := "foreign"
			if textOf(server, "role") == "iran" {
				role = "Iran"
			}
			rows = append(rows, []string{
				orText(server, "name", "?"), role, orText(server, "ip", ""),
				mark(textOf(server, "status")), orText(server, "engine_version", "—"),
			})
		}
		out.Table(rows, []string{"name", "role", "address", "state", "engine"})
	}
	if len(links) > 0 {
		out.Title("Tunnels")
		rows := [][]string{}
		for _, link := range links {
			latency := "—"
			if value, there := link["latency_ms"].(float64); there && value > 0 {
				latency = fmt.Sprintf("%.0f ms", value)
			}
			ports := listOf(link, "exposed")
			if len(ports) == 0 {
				ports = listOf(link, "ports")
			}
			names := []string{}
			for _, port := range ports {
				names = append(names, textOfAny(port))
			}
			rows = append(rows, []string{
				orText(link, "label", orText(link, "iran_name", "")),
				orText(link, "transport", "—"), mark(textOf(link, "status")),
				latency, orDash(strings.Join(names, ", ")),
			})
		}
		out.Table(rows, []string{"tunnel", "carrier", "state", "latency", "ports"})
	}
	if len(domains) > 0 {
		out.Title("Domains")
		rows := [][]string{}
		for _, domain := range domains {
			names := []string{}
			for _, spec := range listOf(domain, "specs") {
				names = append(names, textOfAny(spec))
			}
			rows = append(rows, []string{
				orText(domain, "fqdn", ""), mark(textOf(domain, "status")),
				orText(domain, "current_ip", "—"), orDash(strings.Join(names, ", ")),
			})
		}
		out.Table(rows, []string{"domain", "state", "record", "ports"})
	}
	return 0, nil
}

// cmdServers is the servers the panel manages.
func cmdServers(panel *Panel, args []string) (int, error) {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "list":
		st, err := panel.state()
		if err != nil {
			return 1, err
		}
		rows := [][]string{}
		for _, server := range listOf(st, "servers") {
			role := "foreign"
			if textOf(server, "role") == "iran" {
				role = "Iran"
			}
			rows = append(rows, []string{
				orText(server, "id", ""), orText(server, "name", "?"), role,
				orText(server, "ip", ""), mark(textOf(server, "status")),
			})
		}
		out.Table(rows, []string{"id", "name", "role", "address", "state"})
		return 0, nil
	case "add":
		name := ask("name", "")
		role := ask("role (iran/kharej)", "kharej")
		ip := ask("address", "")
		if name == "" || ip == "" {
			return 1, panelError("the name and the address are needed")
		}
		if _, err := panel.call(http.MethodPost, "/api/servers", map[string]any{
			"name": name, "role": role, "ip": ip,
		}); err != nil {
			return 1, err
		}
		out.OK(fmt.Sprintf("the server %s was added", name))
		return 0, nil
	case "test":
		if len(args) < 2 {
			return 1, panelError("which server? (its id)")
		}
		answer, err := panel.call(http.MethodPost, "/api/servers/"+args[1]+"/test", map[string]any{})
		if err != nil {
			return 1, err
		}
		if latency, there := answer["latency_ms"].(float64); there {
			out.OK(fmt.Sprintf("the server answers in %.0f ms", latency))
			return 0, nil
		}
		out.OK("the server answers")
		return 0, nil
	case "remove":
		if len(args) < 2 {
			return 1, panelError("which server? (its id)")
		}
		if !confirm(fmt.Sprintf("remove the server %s?", args[1])) {
			out.Warn("nothing was removed")
			return 0, nil
		}
		if _, err := panel.call(http.MethodDelete, "/api/servers/"+args[1], nil); err != nil {
			return 1, err
		}
		out.OK("the server was removed")
		return 0, nil
	}
	return 2, panelError("unknown action: %s", action)
}

// cmdTunnels is the tunnels of the panel.
func cmdTunnels(panel *Panel, args []string) (int, error) {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "list":
		st, err := panel.state()
		if err != nil {
			return 1, err
		}
		rows := [][]string{}
		for _, link := range listOf(st, "links") {
			rows = append(rows, []string{
				orText(link, "id", ""), orText(link, "iran_name", ""),
				orText(link, "kharej_name", ""), orText(link, "transport", "—"),
				mark(textOf(link, "status")),
			})
		}
		out.Table(rows, []string{"id", "iran", "kharej", "carrier", "state"})
		return 0, nil
	case "build", "rebuild":
		if len(args) > 1 {
			if _, err := panel.call(http.MethodPost, "/api/discover", map[string]any{
				"link_ids": []string{args[1]},
			}); err != nil {
				return 1, err
			}
			out.OK("the tunnel is being built")
			return 0, nil
		}
		if _, err := panel.call(http.MethodPost, "/api/discover", map[string]any{}); err != nil {
			return 1, err
		}
		out.OK("the tunnels are being built")
		return 0, nil
	case "remove":
		if len(args) < 2 {
			return 1, panelError("which tunnel? (its id)")
		}
		if !confirm(fmt.Sprintf("remove the tunnel %s?", args[1])) {
			out.Warn("nothing was removed")
			return 0, nil
		}
		if _, err := panel.call(http.MethodDelete, "/api/links/"+args[1], nil); err != nil {
			return 1, err
		}
		out.OK("the tunnel was removed")
		return 0, nil
	}
	return 2, panelError("unknown action: %s", action)
}

// cmdDomains is the domains of the panel.
func cmdDomains(panel *Panel, args []string) (int, error) {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "list":
		st, err := panel.state()
		if err != nil {
			return 1, err
		}
		rows := [][]string{}
		for _, domain := range listOf(st, "domains") {
			rows = append(rows, []string{
				orText(domain, "id", ""), orText(domain, "fqdn", ""),
				orText(domain, "current_ip", "—"), mark(textOf(domain, "status")),
			})
		}
		out.Table(rows, []string{"id", "domain", "record", "state"})
		return 0, nil
	case "add":
		fqdn := ask("domain", "")
		provider := ask("the id of the DNS account", "")
		if fqdn == "" || provider == "" {
			return 1, panelError("the domain and the DNS account are needed")
		}
		if _, err := panel.call(http.MethodPost, "/api/domains", map[string]any{
			"fqdn": fqdn, "provider_id": provider,
		}); err != nil {
			return 1, err
		}
		out.OK(fmt.Sprintf("the domain %s was added", fqdn))
		return 0, nil
	case "switch":
		if len(args) < 3 {
			return 1, panelError("which domain and which server? (their ids)")
		}
		if _, err := panel.call(http.MethodPost, "/api/domains/"+args[1]+"/switch", map[string]any{
			"iran_id": args[2],
		}); err != nil {
			return 1, err
		}
		out.OK("the record is being changed")
		return 0, nil
	case "sync":
		if len(args) < 2 {
			return 1, panelError("which domain? (its id)")
		}
		if _, err := panel.call(http.MethodPost, "/api/domains/"+args[1]+"/sync", map[string]any{}); err != nil {
			return 1, err
		}
		out.OK("the record is being read")
		return 0, nil
	case "remove":
		if len(args) < 2 {
			return 1, panelError("which domain? (its id)")
		}
		if !confirm(fmt.Sprintf("remove the domain %s?", args[1])) {
			out.Warn("nothing was removed")
			return 0, nil
		}
		if _, err := panel.call(http.MethodDelete, "/api/domains/"+args[1], nil); err != nil {
			return 1, err
		}
		out.OK("the domain was removed")
		return 0, nil
	}
	return 2, panelError("unknown action: %s", action)
}

// cmdDNS is the DNS accounts of the panel.
func cmdDNS(panel *Panel, args []string) (int, error) {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "list":
		st, err := panel.state()
		if err != nil {
			return 1, err
		}
		rows := [][]string{}
		for _, provider := range listOf(st, "providers") {
			rows = append(rows, []string{
				orText(provider, "id", ""), orText(provider, "name", "?"),
				orText(provider, "kind", ""), orText(provider, "zone", ""),
			})
		}
		out.Table(rows, []string{"id", "name", "service", "zone"})
		return 0, nil
	case "add":
		name := ask("name", "")
		kind := ask("service (cloudflare/arvan)", "cloudflare")
		zone := ask("zone", "")
		token := ask("token", "")
		if name == "" || zone == "" || token == "" {
			return 1, panelError("the name, the zone and the token are needed")
		}
		if _, err := panel.call(http.MethodPost, "/api/providers", map[string]any{
			"name": name, "kind": kind, "zone": zone, "api_token": token,
		}); err != nil {
			return 1, err
		}
		out.OK(fmt.Sprintf("the account %s was added", name))
		return 0, nil
	case "test":
		if len(args) < 2 {
			return 1, panelError("which account? (its id)")
		}
		answer, err := panel.call(http.MethodPost, "/api/providers/"+args[1]+"/verify", map[string]any{})
		if err != nil {
			return 1, err
		}
		out.OK(orText(answer, "detail", "the account works"))
		return 0, nil
	case "remove":
		if len(args) < 2 {
			return 1, panelError("which account? (its id)")
		}
		if !confirm(fmt.Sprintf("remove the account %s?", args[1])) {
			out.Warn("nothing was removed")
			return 0, nil
		}
		if _, err := panel.call(http.MethodDelete, "/api/providers/"+args[1], nil); err != nil {
			return 1, err
		}
		out.OK("the account was removed")
		return 0, nil
	}
	return 2, panelError("unknown action: %s", action)
}

// cmdEngine is the tunnel engine on the servers.
func cmdEngine(panel *Panel, args []string) (int, error) {
	st, err := panel.state()
	if err != nil {
		return 1, err
	}
	rows := [][]string{}
	for _, server := range listOf(st, "servers") {
		rows = append(rows, []string{
			orText(server, "name", "?"), orText(server, "ip", ""),
			orText(server, "engine_version", "—"), mark(textOf(server, "status")),
		})
	}
	out.Table(rows, []string{"server", "address", "engine", "state"})
	return 0, nil
}

// cmdBackup makes, lists and restores the backups.
func cmdBackup(panel *Panel, args []string) (int, error) {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "list":
		answer, err := panel.call(http.MethodGet, "/api/backups", nil)
		if err != nil {
			return 1, err
		}
		rows := [][]string{}
		for _, file := range listOf(answer, "files") {
			rows = append(rows, []string{
				orText(file, "name", ""),
				fmt.Sprintf("%d", int(numberOf(file, "size"))),
				orText(file, "mtime", ""),
			})
		}
		out.Table(rows, []string{"name", "size", "when"})
		return 0, nil
	case "run":
		answer, err := panel.call(http.MethodPost, "/api/backups/run", map[string]any{})
		if err != nil {
			return 1, err
		}
		out.OK("the backup " + orText(answer, "name", "") + " was written")
		return 0, nil
	case "restore":
		if len(args) < 2 {
			return 1, panelError("which backup? (its name)")
		}
		password := ""
		for i := 0; i < len(args); i++ {
			if args[i] == "--password" && i+1 < len(args) {
				password = args[i+1]
				i++
			}
		}
		if !confirm(fmt.Sprintf("restore the backup %s? The state it replaces is kept.", args[1])) {
			out.Warn("nothing was restored")
			return 0, nil
		}
		if _, err := panel.call(http.MethodPost, "/api/backups/restore", map[string]any{
			"name": args[1], "password": password,
		}); err != nil {
			return 1, err
		}
		out.OK("the backup was restored")
		return 0, nil
	}
	return 2, panelError("unknown action: %s", action)
}

// cmdService is the service of the panel.
func cmdService(panel *Panel, args []string) (int, error) {
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "status":
		out.Info("the service of the panel is managed by systemd: systemctl status bull-tunnel")
		return 0, nil
	case "start", "stop", "restart":
		command := exec.Command("systemctl", action, "bull-tunnel")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return 1, panelError("the service could not be %sed: %v", action, err)
		}
		out.OK("the service was " + action + "ed")
		return 0, nil
	}
	return 2, panelError("unknown action: %s", action)
}

// cmdLogs is the log of the panel.
func cmdLogs(panel *Panel, args []string) (int, error) {
	command := exec.Command("journalctl", "-u", "bull-tunnel", "-n", "200", "--no-pager")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return 1, panelError("the log could not be read: %v", err)
	}
	return 0, nil
}

// cmdPasswd sets the admin password.
func cmdPasswd(panel *Panel, args []string) (int, error) {
	username := ask("the name of the operator", "admin")
	password, err := askSecret("the new password")
	if err != nil {
		return 1, panelError("the password was not read: %v", err)
	}
	if password == "" {
		return 1, panelError("the password cannot be empty")
	}
	again, err := askSecret("again")
	if err != nil {
		return 1, panelError("the password was not read: %v", err)
	}
	if again != password {
		return 1, panelError("the two passwords are not the same")
	}
	// the panel's own API does not change the password: the file is written
	// directly, the way the command line does it.
	path := filepath.Join(dataDir(), "auth.json")
	if err := writePassword(path, username, password); err != nil {
		return 1, err
	}
	out.OK("the password was changed")
	return 0, nil
}

// cmdDoctor checks the installation.
func cmdDoctor(panel *Panel, args []string) (int, error) {
	out.Title("bull-Tunnel doctor")
	problems := 0
	panelOK := true
	if _, err := panel.state(); err != nil {
		out.Bad("the panel does not answer: " + err.Error())
		panelOK = false
		problems++
	} else {
		out.OK("the panel answers at " + panel.Base)
	}
	if _, err := os.Stat(keyFile()); err != nil {
		out.Warn("the management key is not there: " + keyFile())
		problems++
	} else {
		out.OK("the management key is there")
	}
	if _, err := os.Stat("/usr/local/bin/bull-tunnel-engine"); err != nil {
		out.Warn("the tunnel engine is not on this machine")
	} else {
		out.OK("the tunnel engine is on this machine")
	}
	if problems > 0 && !panelOK {
		return 1, nil
	}
	return 0, nil
}

// ------------------------------------------------------------------- helpers

// listOf is a list in the answer of the panel.
func listOf(answer map[string]any, key string) []map[string]any {
	raw, there := answer[key].([]any)
	if !there {
		return nil
	}
	out := []map[string]any{}
	for _, item := range raw {
		if entry, ok := item.(map[string]any); ok {
			out = append(out, entry)
		}
	}
	return out
}

// textOf is a text in the answer of the panel.
func textOf(answer map[string]any, key string) string {
	value, there := answer[key].(string)
	if !there {
		return ""
	}
	return value
}

// orText is a text in the answer of the panel, or something else.
func orText(answer map[string]any, key string, fallback string) string {
	if value := textOf(answer, key); value != "" {
		return value
	}
	return fallback
}

// textOfAny is the text of a value of any kind.
func textOfAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return fmt.Sprintf("%v", value)
}

// orDash is a text, or a dash when it is empty.
func orDash(text string) string {
	if text == "" {
		return "—"
	}
	return text
}

// numberOf is a number in the answer of the panel.
func numberOf(answer map[string]any, key string) float64 {
	value, _ := answer[key].(float64)
	return value
}
