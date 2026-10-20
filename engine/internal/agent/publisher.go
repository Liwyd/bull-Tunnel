package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Publisher writes a TXT record with the API of the DNS provider. The
// agent only ever writes into the zone of the command channel, which
// should be a zone of its own.
type Publisher interface {
	// SetTXT writes the text of a record, replacing what was there.
	SetTXT(ctx context.Context, name, text string) error
}

// dnsAPI is the part of a provider the agent needs.
type dnsAPI struct {
	kind   string
	zone   string
	key    string
	client *http.Client
}

// NewPublisher returns the publisher of a provider.
func NewPublisher(kind, zone, key string) (Publisher, error) {
	switch strings.ToLower(kind) {
	case "cloudflare", "arvan", "":
		return &dnsAPI{kind: strings.ToLower(kind), zone: zone, key: key, client: &http.Client{Timeout: 30 * time.Second}}, nil
	}
	return nil, fmt.Errorf("the dns provider %q is not known to the agent", kind)
}

func (d *dnsAPI) headers() map[string]string {
	switch d.kind {
	case "arvan":
		key := strings.TrimSpace(d.key)
		if !strings.HasPrefix(strings.ToLower(key), "apikey ") {
			key = "Apikey " + key
		}
		return map[string]string{"Authorization": key, "Accept": "application/json", "Content-Type": "application/json"}
	default:
		return map[string]string{"Authorization": "Bearer " + d.key, "Content-Type": "application/json"}
	}
}

// SetTXT writes the text of a TXT record.
func (d *dnsAPI) SetTXT(ctx context.Context, name, text string) error {
	switch d.kind {
	case "arvan":
		return d.setArvan(ctx, name, text)
	default:
		return d.setCloudflare(ctx, name, text)
	}
}

func (d *dnsAPI) setCloudflare(ctx context.Context, name, text string) error {
	const api = "https://api.cloudflare.com/client/v4"
	zid, err := d.cloudflareZone(ctx, api)
	if err != nil {
		return err
	}
	recs, err := d.get(ctx, api+fmt.Sprintf("/zones/%s/dns_records?type=TXT&name=%s", zid, name))
	if err != nil {
		return err
	}
	var list []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recs, &list); err != nil {
		return err
	}
	// cloudflare wants the text of a TXT record quoted
	body := map[string]any{
		"type":    "TXT",
		"name":    name,
		"content": `"` + text + `"`,
		"ttl":     60,
	}
	var out []byte
	if len(list) == 0 {
		out, err = d.send(ctx, "POST", api+fmt.Sprintf("/zones/%s/dns_records", zid), body)
	} else {
		out, err = d.send(ctx, "PUT", api+fmt.Sprintf("/zones/%s/dns_records/%s", zid, list[0].ID), body)
	}
	if err != nil {
		return err
	}
	return d.check(out)
}

func (d *dnsAPI) cloudflareZone(ctx context.Context, api string) (string, error) {
	blob, err := d.get(ctx, api+"/zones?name="+d.zone)
	if err != nil {
		return "", err
	}
	var zones []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(blob, &zones); err != nil {
		return "", err
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("the zone %s was not found for this key", d.zone)
	}
	return zones[0].ID, nil
}

func (d *dnsAPI) setArvan(ctx context.Context, name, text string) error {
	const api = "https://napi.arvancloud.ir/cdn/4.0"
	recs, err := d.get(ctx, api+fmt.Sprintf("/domains/%s/dns-records?search=%s&per_page=100", d.zone, name))
	if err != nil {
		return err
	}
	var list []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(recs, &list); err != nil {
		return err
	}
	body := map[string]any{
		"type":  "TXT",
		"name":  name,
		"value": map[string]string{"text": text},
		"ttl":   120,
	}
	var out []byte
	var id string
	for _, r := range list {
		if strings.EqualFold(r.Type, "txt") && r.Name == name {
			id = r.ID
			break
		}
	}
	if id == "" {
		out, err = d.send(ctx, "POST", api+fmt.Sprintf("/domains/%s/dns-records", d.zone), body)
	} else {
		out, err = d.send(ctx, "PUT", api+fmt.Sprintf("/domains/%s/dns-records/%s", d.zone, id), body)
	}
	if err != nil {
		return err
	}
	_ = out
	return nil
}

func (d *dnsAPI) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range d.headers() {
		req.Header.Set(k, v)
	}
	r, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	blob, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if r.StatusCode >= 400 {
		return nil, fmt.Errorf("the dns api answered %d: %s", r.StatusCode, strings.TrimSpace(string(blob))[:min(200, len(blob))])
	}
	return blob, nil
}

func (d *dnsAPI) send(ctx context.Context, method, url string, body any) ([]byte, error) {
	blob, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(blob))
	if err != nil {
		return nil, err
	}
	for k, v := range d.headers() {
		req.Header.Set(k, v)
	}
	r, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	out, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if r.StatusCode >= 400 {
		return nil, fmt.Errorf("the dns api answered %d: %s", r.StatusCode, strings.TrimSpace(string(out))[:min(200, len(out))])
	}
	return out, nil
}

// check reads the answer of cloudflare and turns it into an error when the
// request was refused.
func (d *dnsAPI) check(blob []byte) error {
	var body struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(blob, &body); err != nil {
		return nil // not cloudflare, or an answer without the wrapper
	}
	if body.Success {
		return nil
	}
	msgs := make([]string, 0, len(body.Errors))
	for _, e := range body.Errors {
		msgs = append(msgs, e.Message)
	}
	return fmt.Errorf("cloudflare refused the record: %s", strings.Join(msgs, "; "))
}

// LookupTXT reads the text of the TXT records of a name with the
// resolvers of the system. No key is needed: the agent only reads. It is
// a variable so the tests can replace it.
var LookupTXT = func(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}
