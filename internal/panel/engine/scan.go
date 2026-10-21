package engine

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// Found is a tunnel configuration found on a server by the scan.
type Found struct {
	Name   string
	Role   string // server | client
	Data   model.Ordered
	Active string
	Unit   string // the systemd unit running it ("" = none found)
}

// Transport is the carrier the tunnel runs on.
func (f *Found) Transport() string {
	if value, there := f.Data.Get("transport"); there {
		return fmt.Sprintf("%v", value)
	}
	return "tcp"
}

// Token is the secret the two ends of the tunnel share.
func (f *Found) Token() string {
	if value, there := f.Data.Get("token"); there {
		text := fmt.Sprintf("%v", value)
		if text != "" {
			return text
		}
	}
	return "bulltunnel" // the engine's built-in default
}

// Port is the port the tunnel listens on (or dials).
func (f *Found) Port() int {
	address := f.address()
	if address == "" {
		return 0
	}
	parts := strings.Split(address, ":")
	value, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0
	}
	return value
}

// Host is the address the tunnel listens on (or dials).
func (f *Found) Host() string {
	address := f.address()
	if address == "" || !strings.Contains(address, ":") {
		return ""
	}
	return strings.Trim(address[:strings.LastIndex(address, ":")], "[]")
}

// address is the address of the tunnel, the one it binds on the Iran side
// and the one it dials on the kharej side.
func (f *Found) address() string {
	key := "bind_addr"
	if f.Role != "server" {
		key = "remote_addr"
	}
	if value, there := f.Data.Get(key); there {
		return fmt.Sprintf("%v", value)
	}
	return ""
}

// ParseScan reads the tunnels the scan script found on a server.
//
// Reverse tunnels in a server's /etc/bull-tunnel. Direct and layer-3
// tunnels, and anything unparsable, are skipped.
func ParseScan(out string) []*Found {
	found := []*Found{}
	for _, chunk := range strings.Split(out, "@@FILE ")[1:] {
		head, body, _ := strings.Cut(chunk, "\n")
		body = strings.SplitN(body, "@@END", 2)[0]
		parts := strings.Fields(head)
		if len(parts) == 0 || !safeName.MatchString(parts[0]) {
			continue
		}
		name := parts[0]
		unit := UnitName(name)
		active := ""
		if len(parts) >= 3 { // name unit active
			unit, active = parts[1], parts[2]
		} else if len(parts) > 1 { // older panel scripts: name active
			active = parts[1]
		}
		if unit == "-" {
			unit = ""
		}
		if active == "-" {
			active = ""
		}
		data, err := ParseTOML(body)
		if err != nil {
			continue
		}
		for _, role := range []string{"server", "client"} {
			value, there := data.Get(role)
			if !there {
				continue
			}
			section, ok := asSection(value)
			if !ok {
				continue
			}
			if transport, there := section.Get("transport"); there && fmt.Sprintf("%v", transport) == "spoof" {
				continue
			}
			found = append(found, &Found{
				Name:   name,
				Role:   role,
				Data:   section,
				Active: strings.TrimSpace(active),
				Unit:   unit,
			})
		}
	}
	return found
}

// ParseTOML reads a configuration file, keeping the order of its keys.
//
// The order is not a detail: the configuration the panel writes for a
// tunnel that was adopted keeps the order of the file it was adopted from,
// so the panel does not shuffle the settings of a tunnel it did not build.
func ParseTOML(text string) (model.Ordered, error) {
	var values map[string]any
	if err := toml.Unmarshal([]byte(text), &values); err != nil {
		return model.Ordered{}, err
	}
	return orderSection(values, tomlOrder(text), ""), nil
}

// tomlOrder is the order of the keys of every table of a configuration
// file, as they are written in it.
func tomlOrder(text string) map[string][]string {
	order := map[string][]string{}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			if end := strings.Index(trimmed, "]"); end > 0 {
				section = strings.TrimSpace(trimmed[1:end])
			}
			continue
		}
		key, _, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || !safeName.MatchString(key) {
			continue
		}
		order[section] = append(order[section], key)
	}
	return order
}

// orderSection writes the values of a table in the order the file has them.
func orderSection(values map[string]any, order map[string][]string, section string) model.Ordered {
	out := model.NewOrdered()
	written := map[string]bool{}
	for _, key := range order[section] {
		value, there := values[key]
		if !there || written[key] {
			continue
		}
		written[key] = true
		if sub, ok := value.(map[string]any); ok {
			out.Set(key, orderSection(sub, order, childSection(section, key)))
			continue
		}
		out.Set(key, value)
	}
	// the keys the scanner did not see (a value on one line, a table that
	// was written in an unusual way) are written at the end.
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		if written[key] {
			continue
		}
		if sub, ok := values[key].(map[string]any); ok {
			out.Set(key, orderSection(sub, order, childSection(section, key)))
			continue
		}
		out.Set(key, values[key])
	}
	return out
}

// childSection is the name of a table inside another one.
func childSection(section string, key string) string {
	if section == "" {
		return key
	}
	return section + "." + key
}

// sortStrings sorts a list of strings.
func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// portsOf is the list of forwarded ports of a configuration.
func portsOf(section model.Ordered) []string {
	value, there := section.Get("ports")
	if !there {
		return nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range list {
		out = append(out, fmt.Sprintf("%v", item))
	}
	return out
}
