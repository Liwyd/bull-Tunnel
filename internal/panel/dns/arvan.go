package dns

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ArvanCloud DNS (CDN API 4.0, a machine user's API key).
const arvanAPI = "https://napi.arvancloud.ir/cdn/4.0"

// What the usual refusals mean, in the panel's language.
var arvanHints = map[int]string{
	401: "the API key is invalid or has expired",
	403: "this key has no access to the DNS section of this domain; in ArvanCloud IAM, grant the machine user " +
		"that owns the key DNS access (read and write) on this domain. If the access is complete, the account itself is " +
		"limited (an unpaid bill, for instance)",
	404: "this zone was not found in the account that owns the key (the zone must be the root domain, e.g. example.com)",
}

// arvan is the client of ArvanCloud.
type arvan struct {
	base
}

// headers are the headers every request carries. The key of ArvanCloud is
// written as it is when the operator pasted it with its prefix, and without
// it when they did not.
func (a *arvan) headers() map[string]string {
	key := strings.TrimSpace(a.provider.APIToken)
	if !strings.HasPrefix(strings.ToLower(key), "apikey ") {
		key = "Apikey " + key
	}
	return map[string]string{"Authorization": key, "Accept": "application/json"}
}

// json reads the answer of ArvanCloud, or says why it refused.
func (a *arvan) json(body []byte, status int) (map[string]any, error) {
	if status >= 400 {
		message := ""
		var answer map[string]any
		if err := json.Unmarshal(body, &answer); err == nil {
			if text, ok := answer["message"].(string); ok && text != "" {
				message = text
			}
		}
		if message == "" {
			message = string(body)
		}
		if len(message) > 200 {
			message = message[:200]
		}
		text := fmt.Sprintf("arvan: HTTP %d: %s", status, message)
		if hint, there := arvanHints[status]; there {
			text += " — " + hint
		}
		return nil, dnsError("%s", text)
	}
	var answer map[string]any
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, dnsError("arvan: the answer is not JSON: %v", err)
	}
	return answer, nil
}

// recordsPath is where the records of the zone are.
func (a *arvan) recordsPath() string {
	return fmt.Sprintf("/domains/%s/dns-records", strings.TrimRight(a.provider.Zone, "."))
}

// aRecords are the A records of a name.
func (a *arvan) aRecords(fqdn string) ([]map[string]any, error) {
	name, err := a.relative(fqdn)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	page := 1
	for {
		body, status, err := a.get(a.apiBase+a.recordsPath(), url.Values{
			"search": {name}, "page": {strconv.Itoa(page)}, "per_page": {"100"},
		}, a.headers())
		if err != nil {
			return nil, err
		}
		answer, err := a.json(body, status)
		if err != nil {
			return nil, err
		}
		for _, item := range listOf(answer["data"]) {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if strings.ToLower(textOf(record["type"])) != "a" || textOf(record["name"]) != name {
				continue
			}
			out = append(out, record)
		}
		meta, _ := answer["meta"].(map[string]any)
		last := 1
		if value, ok := meta["last_page"].(float64); ok {
			last = int(value)
		}
		if page >= last {
			return out, nil
		}
		page++
	}
}

// GetA is the addresses a name points at.
func (a *arvan) GetA(fqdn string) ([]string, error) {
	records, err := a.aRecords(fqdn)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, record := range records {
		value := record["value"]
		// the value of an A record is a list of addresses; some accounts
		// answer with a single one, written out
		if single, ok := value.(map[string]any); ok {
			value = []any{single}
		}
		for _, item := range listOf(value) {
			if entry, ok := item.(map[string]any); ok {
				if ip := textOf(entry["ip"]); ip != "" {
					out = append(out, ip)
				}
			}
		}
	}
	return out, nil
}

// SetA points a name at an address.
func (a *arvan) SetA(fqdn string, ip string, ttl int) error {
	name, err := a.relative(fqdn)
	if err != nil {
		return err
	}
	if ttl < 120 {
		ttl = 120 // Arvan's minimum
	}
	body, _ := json.Marshal(map[string]any{
		"type":           "a",
		"name":           name,
		"value":          []any{map[string]any{"ip": ip, "port": nil, "weight": 100, "country": ""}},
		"ttl":            ttl,
		"cloud":          a.provider.Proxied,
		"upstream_https": "default",
		"ip_filter_mode": map[string]any{"count": "single", "order": "none", "geo_filter": "none"},
	})
	records, err := a.aRecords(fqdn)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		answer, status, err := a.post(a.apiBase+a.recordsPath(), body, a.headers())
		if err != nil {
			return err
		}
		_, err = a.json(answer, status)
		return err
	}
	first := textOf(records[0]["id"])
	answer, status, err := a.put(fmt.Sprintf("%s/%s", a.apiBase+a.recordsPath(), first), body, a.headers())
	if err != nil {
		return err
	}
	if _, err := a.json(answer, status); err != nil {
		return err
	}
	for _, extra := range records[1:] {
		id := textOf(extra["id"])
		answer, status, err := a.delete(fmt.Sprintf("%s/%s", a.apiBase+a.recordsPath(), id), a.headers())
		if err != nil {
			return err
		}
		if _, err := a.json(answer, status); err != nil {
			return err
		}
	}
	return nil
}

// GetTXT is the text of the TXT records of a name.
func (a *arvan) GetTXT(fqdn string) ([]string, error) {
	name, err := a.relative(fqdn)
	if err != nil {
		return nil, err
	}
	body, status, err := a.get(a.apiBase+a.recordsPath(), url.Values{
		"search": {name}, "per_page": {"100"},
	}, a.headers())
	if err != nil {
		return nil, err
	}
	answer, err := a.json(body, status)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, item := range listOf(answer["data"]) {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.ToLower(textOf(record["type"])) != "txt" || textOf(record["name"]) != name {
			continue
		}
		value := record["value"]
		if single, ok := value.(map[string]any); ok {
			if text := textOf(single["text"]); text != "" {
				out = append(out, text)
			}
			continue
		}
		for _, entry := range listOf(value) {
			switch v := entry.(type) {
			case map[string]any:
				if text := textOf(v["text"]); text != "" {
					out = append(out, text)
				}
			case string:
				out = append(out, v)
			}
		}
	}
	return out, nil
}

// SetTXT writes one TXT record, replacing what was there.
func (a *arvan) SetTXT(fqdn string, text string, ttl int) error {
	name, err := a.relative(fqdn)
	if err != nil {
		return err
	}
	if ttl < 120 {
		ttl = 120 // Arvan's minimum
	}
	body, _ := json.Marshal(map[string]any{
		"type":  "TXT",
		"name":  name,
		"value": map[string]any{"text": text},
		"ttl":   ttl,
	})
	answer, status, err := a.get(a.apiBase+a.recordsPath(), url.Values{
		"search": {name}, "per_page": {"100"},
	}, a.headers())
	if err != nil {
		return err
	}
	records, err := a.json(answer, status)
	if err != nil {
		return err
	}
	existing := ""
	for _, item := range listOf(records["data"]) {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.ToLower(textOf(record["type"])) == "txt" && textOf(record["name"]) == name {
			existing = textOf(record["id"])
			break
		}
	}
	if existing == "" {
		answer, status, err := a.post(a.apiBase+a.recordsPath(), body, a.headers())
		if err != nil {
			return err
		}
		_, err = a.json(answer, status)
		return err
	}
	answer, status, err = a.put(fmt.Sprintf("%s/%s", a.apiBase+a.recordsPath(), existing), body, a.headers())
	if err != nil {
		return err
	}
	_, err = a.json(answer, status)
	return err
}

// Verify checks the credentials and the zone.
func (a *arvan) Verify() (string, error) {
	body, status, err := a.get(a.apiBase+a.recordsPath(), url.Values{"per_page": {"1"}}, a.headers())
	if err != nil {
		return "", err
	}
	if _, err := a.json(body, status); err != nil {
		return "", err
	}
	return fmt.Sprintf("zone %s", a.provider.Zone), nil
}

// listOf is a list in an answer of the service.
func listOf(value any) []any {
	list, _ := value.([]any)
	return list
}

// textOf is a string in an answer of the service.
func textOf(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}
