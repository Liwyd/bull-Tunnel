package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The configuration the panel writes is compared with the one the Python
// panel writes, key by key and value by value. So the TOML is written by
// hand, the way the Python panel writes it: a value on its own line, a list
// with four spaces of indentation, and the tables after the keys.

// tomlValue is one value of a TOML file.
func tomlValue(value any) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return tomlFloat(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return strconv.FormatInt(i, 10)
		}
		f, _ := v.Float64()
		return tomlFloat(f)
	case string:
		return tomlString(v)
	case []string:
		return tomlList(len(v), func(i int) string { return tomlString(v[i]) })
	case []any:
		return tomlList(len(v), func(i int) string { return tomlValue(v[i]) })
	default:
		return tomlString(fmt.Sprintf("%v", v))
	}
}

// tomlFloat writes a number the way Python writes it: a whole number keeps
// its point, so that a port stays a port.
func tomlFloat(value float64) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.Contains(text, ".") && !strings.Contains(text, "e") {
		text += ".0"
	}
	return text
}

// tomlString writes a string in quotes.
func tomlString(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n")
	return "\"" + replacer.Replace(value) + "\""
}

// tomlList writes a list, one element on its own line.
func tomlList(length int, element func(int) string) string {
	if length == 0 {
		return "[]"
	}
	var out strings.Builder
	out.WriteString("[\n")
	for i := 0; i < length; i++ {
		out.WriteString("    ")
		out.WriteString(element(i))
		out.WriteString(",\n")
	}
	out.WriteString("]")
	return out.String()
}

// dumpSection writes a table and the tables inside it.
func dumpSection(name string, data *model.Ordered, header string) string {
	lines := []string{}
	if header != "" {
		lines = append(lines, commentLine(header))
	}
	lines = append(lines, "["+name+"]")
	subs := []struct {
		key   string
		value model.Ordered
	}{}
	for _, key := range data.Keys() {
		value, _ := data.Get(key)
		if sub, ok := asSection(value); ok {
			subs = append(subs, struct {
				key   string
				value model.Ordered
			}{key, sub})
			continue
		}
		lines = append(lines, key+" = "+tomlValue(value))
	}
	out := strings.Join(lines, "\n") + "\n"
	for _, sub := range subs {
		out += "\n" + dumpSection(name+"."+sub.key, &sub.value, "")
	}
	return out
}

// commentLine keeps a header on the one line it was written for: it is
// written as a TOML comment at the top of the configuration, and a name
// carrying a line break would put the line after it into that
// configuration as a key of its own.
func commentLine(line string) string {
	line = strings.ReplaceAll(line, "\r\n", " ")
	line = strings.ReplaceAll(line, "\n", " ")
	return strings.ReplaceAll(line, "\r", " ")
}

// asSection is a value of a table when it is a table itself.
func asSection(value any) (model.Ordered, bool) {
	switch v := value.(type) {
	case model.Ordered:
		return v, true
	case *model.Ordered:
		if v == nil {
			return model.Ordered{}, false
		}
		return *v, true
	case map[string]any:
		// a map has no order of its own: the keys are sorted, so the file
		// is at least the same every time it is written.
		out := model.NewOrdered()
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out.Set(key, v[key])
		}
		return out, true
	}
	return model.Ordered{}, false
}

// serverSection is the [server] table of a tunnel: the Iran side.
func serverSection(link *model.Link, transport string, ports []string, kharej *model.Server) *model.Ordered {
	d := link.IranBase.Clone()
	host := "0.0.0.0"
	if value, there := d.Get("bind_addr"); there {
		host = hostBeforePort(fmt.Sprintf("%v", value))
	}
	if host == "" {
		host = "0.0.0.0"
	}
	d.Set("bind_addr", fmt.Sprintf("%s:%d", host, link.TunnelPort))
	d.Set("transport", transport)
	d.Set("token", link.Token)
	d.Set("accept_udp", link.AcceptUDP)
	// Zero leaves what the engine already has: the kernel's own value for a
	// tunnel the panel built, and the setting of a tunnel it adopted.
	if link.MSS > 0 {
		d.Set("mss", link.MSS)
	}
	// The heartbeat is the Iran side's alone: it is the server that sends
	// the liveness byte down the control channel, and the client's own
	// config has no such key. Zero writes no line, and the engine keeps
	// its own rhythm.
	if link.Heartbeat > 0 {
		d.Set("heartbeat", link.Heartbeat)
	}
	if !d.Has("log_level") {
		d.Set("log_level", "info")
	}
	probe := []string{}
	if link.ProbePort != 0 {
		probe = append(probe, fmt.Sprintf("127.0.0.1:%d=%s", link.ProbePort, kharej.ProbeAddr()))
	}
	d.Set("ports", append(probe, ports...))
	return &d
}

// clientSection is the [client] table of a tunnel: the kharej side.
func clientSection(link *model.Link, transport string, iran *model.Server) *model.Ordered {
	d := link.KharejBase.Clone()
	baseHost := ""
	if value, there := d.Get("remote_addr"); there {
		text := fmt.Sprintf("%v", value)
		if strings.Contains(text, ":") {
			baseHost = text[:strings.LastIndex(text, ":")]
		}
	}
	host := baseHost
	if host == "" {
		if strings.Contains(iran.IP, ":") {
			host = "[" + iran.IP + "]"
		} else {
			host = iran.IP
		}
	}
	d.Set("remote_addr", fmt.Sprintf("%s:%d", host, link.TunnelPort))
	d.Set("transport", transport)
	d.Set("token", link.Token)
	if link.MSS > 0 {
		d.Set("mss", link.MSS)
	}
	if !d.Has("connection_pool") {
		d.Set("connection_pool", 8)
	}
	if !d.Has("log_level") {
		d.Set("log_level", "info")
	}
	return &d
}

// hostBeforePort is the address of an "address:port" pair.
func hostBeforePort(address string) string {
	if i := strings.LastIndex(address, ":"); i >= 0 {
		return address[:i]
	}
	return ""
}

// RenderServer is the configuration of the Iran side of a tunnel.
func RenderServer(link *model.Link, transport string, ports []string, kharej *model.Server) string {
	head := fmt.Sprintf("# Managed by bull-Tunnel (link %s) - kharej %s -> this Iran server", link.ID, kharej.Name)
	return dumpSection("server", serverSection(link, transport, ports, kharej), head)
}

// RenderClient is the configuration of the kharej side of a tunnel.
func RenderClient(link *model.Link, transport string, iran *model.Server) string {
	head := fmt.Sprintf("# Managed by bull-Tunnel (link %s) - this kharej server -> Iran %s", link.ID, iran.Name)
	return dumpSection("client", clientSection(link, transport, iran), head)
}

// UnitName is the name of the unit of a tunnel.
func UnitName(name string) string {
	return BaseUnit + "-" + name + ".service"
}

// IranUnit is the unit that runs the Iran side of a tunnel.
func IranUnit(link *model.Link) string {
	if link.IranUnit != "" {
		return link.IranUnit
	}
	return UnitName(link.IranName)
}

// KharejUnit is the unit that runs the kharej side of a tunnel.
func KharejUnit(link *model.Link) string {
	if link.KharejUnit != "" {
		return link.KharejUnit
	}
	return UnitName(link.KharejName)
}

// ConfPath is where the configuration of a tunnel is on the server.
func ConfPath(name string) string {
	return ConfDir + "/" + name + ".toml"
}

// RenderUnit is the systemd unit that runs a tunnel.
func RenderUnit(name string) string {
	return fmt.Sprintf(`[Unit]
Description=bull-Tunnel tunnel (%s)
After=network.target

[Service]
Type=simple
ExecStart=%s -c %s
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`, name, BinPath, ConfPath(name))
}
