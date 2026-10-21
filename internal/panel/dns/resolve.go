package dns

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
)

// ResolveA asks the public resolvers (DNS-over-HTTPS, the JSON API) what a
// name points at, to tell when a record change has actually reached users.
//
// The answer is the resolver and what it said: nil when that resolver could
// not be asked.
func ResolveA(fqdn string, resolvers []string, proxyURL string) map[string][]string {
	client, err := httpClient(proxyURL)
	if err != nil {
		out := map[string][]string{}
		for _, resolver := range resolvers {
			out[resolver] = nil
		}
		return out
	}
	client.Timeout = 8 * 1e9 // eight seconds, as the Python panel
	out := map[string][]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, resolver := range resolvers {
		wg.Add(1)
		go func(resolver string) {
			defer wg.Done()
			answer := resolveOne(client, resolver, fqdn)
			mu.Lock()
			out[resolver] = answer
			mu.Unlock()
		}(resolver)
	}
	wg.Wait()
	return out
}

// resolveOne asks one resolver what a name points at.
func resolveOne(client *http.Client, resolver string, fqdn string) []string {
	query := url.Values{"name": {fqdn}, "type": {"A"}}
	request, err := http.NewRequest(http.MethodGet, resolver+"?"+query.Encode(), nil)
	if err != nil {
		return nil
	}
	request.Header.Set("accept", "application/dns-json")
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	var body struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return nil
	}
	out := []string{}
	for _, answer := range body.Answer {
		if answer.Type == 1 {
			out = append(out, answer.Data)
		}
	}
	return out
}
