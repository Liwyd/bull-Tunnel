package dns

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Cloudflare DNS (API v4, an API token with Zone:DNS:Edit).
const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// cloudflare is the client of Cloudflare.
type cloudflare struct {
	base
}

// headers are the headers every request carries.
func (b *base) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + b.provider.APIToken}
}

// unwrap reads the answer of Cloudflare: the result, or the reason the
// service refused.
func (b *base) unwrap(body []byte, status int) (any, error) {
	var answer struct {
		Success bool `json:"success"`
		Result  any  `json:"result"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, dnsError("cloudflare: HTTP %d", status)
	}
	if !answer.Success {
		messages := []string{}
		for _, e := range answer.Errors {
			messages = append(messages, e.Message)
		}
		text := strings.Join(messages, "; ")
		if text == "" {
			text = fmt.Sprintf("HTTP %d", status)
		}
		return nil, dnsError("cloudflare: %s", text)
	}
	return answer.Result, nil
}

// zoneID is the identifier of the zone of the account.
func (c *cloudflare) zoneID() (string, error) {
	if c.zone != "" {
		return c.zone, nil
	}
	body, status, err := c.get(c.apiBase+"/zones", url.Values{"name": {strings.TrimRight(c.provider.Zone, ".")}}, c.headers())
	if err != nil {
		return "", err
	}
	result, err := c.unwrap(body, status)
	if err != nil {
		return "", err
	}
	zones, _ := result.([]any)
	if len(zones) == 0 {
		return "", dnsError("cloudflare: zone %s not found for this token", c.provider.Zone)
	}
	zone, _ := zones[0].(map[string]any)
	id, _ := zone["id"].(string)
	c.zone = id
	return id, nil
}

// records are the records of a name, of one kind.
func (c *cloudflare) records(zoneID string, kind string, fqdn string) ([]map[string]any, error) {
	body, status, err := c.get(
		fmt.Sprintf("%s/zones/%s/dns_records", c.apiBase, zoneID),
		url.Values{"type": {kind}, "name": {strings.TrimRight(fqdn, ".")}},
		c.headers())
	if err != nil {
		return nil, err
	}
	result, err := c.unwrap(body, status)
	if err != nil {
		return nil, err
	}
	list, _ := result.([]any)
	out := []map[string]any{}
	for _, item := range list {
		if record, ok := item.(map[string]any); ok {
			out = append(out, record)
		}
	}
	return out, nil
}

// GetA is the addresses a name points at.
func (c *cloudflare) GetA(fqdn string) ([]string, error) {
	zoneID, err := c.zoneID()
	if err != nil {
		return nil, err
	}
	records, err := c.records(zoneID, "A", fqdn)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, record := range records {
		if content, ok := record["content"].(string); ok {
			out = append(out, content)
		}
	}
	return out, nil
}

// SetA points a name at an address.
func (c *cloudflare) SetA(fqdn string, ip string, ttl int) error {
	if _, err := c.relative(fqdn); err != nil {
		return err
	}
	ttlValue := ttl
	if c.provider.Proxied {
		ttlValue = 1
	} else if ttlValue < 60 {
		ttlValue = 60
	}
	body, _ := json.Marshal(map[string]any{
		"type":    "A",
		"name":    strings.TrimRight(fqdn, "."),
		"content": ip,
		"ttl":     ttlValue,
		"proxied": c.provider.Proxied,
	})
	zoneID, err := c.zoneID()
	if err != nil {
		return err
	}
	records, err := c.records(zoneID, "A", fqdn)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		answer, status, err := c.post(fmt.Sprintf("%s/zones/%s/dns_records", c.apiBase, zoneID), body, c.headers())
		if err != nil {
			return err
		}
		_, err = c.unwrap(answer, status)
		return err
	}
	first, _ := records[0]["id"].(string)
	answer, status, err := c.put(fmt.Sprintf("%s/zones/%s/dns_records/%s", c.apiBase, zoneID, first), body, c.headers())
	if err != nil {
		return err
	}
	if _, err := c.unwrap(answer, status); err != nil {
		return err
	}
	// one A record per name: a stale second record would keep sending half
	// the users to the dead server
	for _, extra := range records[1:] {
		id, _ := extra["id"].(string)
		answer, status, err := c.delete(fmt.Sprintf("%s/zones/%s/dns_records/%s", c.apiBase, zoneID, id), c.headers())
		if err != nil {
			return err
		}
		if _, err := c.unwrap(answer, status); err != nil {
			return err
		}
	}
	return nil
}

// GetTXT is the text of the TXT records of a name.
func (c *cloudflare) GetTXT(fqdn string) ([]string, error) {
	zoneID, err := c.zoneID()
	if err != nil {
		return nil, err
	}
	records, err := c.records(zoneID, "TXT", fqdn)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, record := range records {
		content, _ := record["content"].(string)
		out = append(out, UnquoteTXT(content))
	}
	return out, nil
}

// SetTXT writes one TXT record, replacing what was there.
func (c *cloudflare) SetTXT(fqdn string, text string, ttl int) error {
	if _, err := c.relative(fqdn); err != nil {
		return err
	}
	if ttl < 60 {
		ttl = 60
	}
	body, _ := json.Marshal(map[string]any{
		"type":    "TXT",
		"name":    strings.TrimRight(fqdn, "."),
		"content": QuoteTXT(text),
		"ttl":     ttl,
		"proxied": false,
	})
	zoneID, err := c.zoneID()
	if err != nil {
		return err
	}
	records, err := c.records(zoneID, "TXT", fqdn)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		answer, status, err := c.post(fmt.Sprintf("%s/zones/%s/dns_records", c.apiBase, zoneID), body, c.headers())
		if err != nil {
			return err
		}
		_, err = c.unwrap(answer, status)
		return err
	}
	first, _ := records[0]["id"].(string)
	answer, status, err := c.put(fmt.Sprintf("%s/zones/%s/dns_records/%s", c.apiBase, zoneID, first), body, c.headers())
	if err != nil {
		return err
	}
	if _, err := c.unwrap(answer, status); err != nil {
		return err
	}
	for _, extra := range records[1:] {
		id, _ := extra["id"].(string)
		answer, status, err := c.delete(fmt.Sprintf("%s/zones/%s/dns_records/%s", c.apiBase, zoneID, id), c.headers())
		if err != nil {
			return err
		}
		if _, err := c.unwrap(answer, status); err != nil {
			return err
		}
	}
	return nil
}

// Verify checks the credentials and the zone.
func (c *cloudflare) Verify() (string, error) {
	zoneID, err := c.zoneID()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("zone %s (%s…)", c.provider.Zone, zoneID[:min(8, len(zoneID))]), nil
}

// readAll reads a reader to the end.
func readAll(reader io.Reader) ([]byte, error) {
	return io.ReadAll(reader)
}
