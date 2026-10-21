// Package dns is the panel's access to the DNS services: Cloudflare and
// ArvanCloud.
//
// The clients ask the same questions in the same words in the two panels:
// the same requests, the same bodies, the same way of quoting the text of a
// TXT record — the command channel lives in one.
package dns

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"golang.org/x/net/proxy"
)

// Error says that the DNS service refused or could not be asked.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func dnsError(format string, args ...any) error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

// Client is what the panel asks of a DNS service.
type Client interface {
	// GetA is the addresses a name points at.
	GetA(fqdn string) ([]string, error)
	// SetA points a name at an address.
	SetA(fqdn string, ip string, ttl int) error
	// GetTXT is the text of the TXT records of a name.
	GetTXT(fqdn string) ([]string, error)
	// SetTXT writes one TXT record, replacing what was there.
	SetTXT(fqdn string, text string, ttl int) error
	// Verify checks the credentials and the zone; it returns a short
	// description of the zone.
	Verify() (string, error)
}

// QuoteTXT is the text of a TXT record as the API wants it: quoted, with
// the quotes inside escaped.
func QuoteTXT(text string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"")
	return "\"" + replacer.Replace(text) + "\""
}

// UnquoteTXT is the text of a TXT record as the API gives it back.
func UnquoteTXT(content string) string {
	s := strings.TrimSpace(content)
	if len(s) >= 2 && strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		s = s[1 : len(s)-1]
	}
	replacer := strings.NewReplacer("\\\"", "\"", "\\\\", "\\")
	return replacer.Replace(s)
}

// Relative is the record name relative to the zone ("@" for the apex).
func Relative(fqdn string, zone string) (string, error) {
	fqdn = strings.ToLower(strings.TrimRight(fqdn, "."))
	zone = strings.ToLower(strings.TrimRight(zone, "."))
	if fqdn == zone {
		return "@", nil
	}
	if !strings.HasSuffix(fqdn, "."+zone) {
		return "", dnsError("%s is not inside zone %s", fqdn, zone)
	}
	return fqdn[:len(fqdn)-len(zone)-1], nil
}

// New returns the client of a DNS account.
func New(provider *model.DNSProvider, proxyURL string) (Client, error) {
	client, err := httpClient(proxyURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(provider.Kind) {
	case "cloudflare", "cf":
		return &cloudflare{base: base{provider: provider, client: client, apiBase: cloudflareAPI}}, nil
	case "arvan", "arvancloud":
		return &arvan{base: base{provider: provider, client: client, apiBase: arvanAPI}}, nil
	}
	return nil, dnsError("unknown DNS service: %s", provider.Kind)
}

// httpClient is the client the panel asks the services with. The timeout is
// the one of the Python panel: twenty seconds.
func httpClient(proxyURL string) (*http.Client, error) {
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 20 * time.Second,
	}
	if proxyURL != "" {
		if strings.HasPrefix(proxyURL, "socks5://") || strings.HasPrefix(proxyURL, "socks5h://") {
			dialer, err := proxy.SOCKS5("tcp", proxyURL, nil, proxy.Direct)
			if err != nil {
				return nil, dnsError("the proxy cannot be used: %v", err)
			}
			transport.DialContext = func(ctx context.Context, network string, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		} else {
			parsed, err := url.Parse(proxyURL)
			if err != nil {
				return nil, dnsError("the proxy cannot be used: %v", err)
			}
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: transport}, nil
}

// base is what the two clients share.
type base struct {
	provider *model.DNSProvider
	client   *http.Client
	apiBase  string
	zone     string
}

// relative is the record name relative to the zone of the account.
func (b *base) relative(fqdn string) (string, error) {
	return Relative(fqdn, b.provider.Zone)
}

// get asks the service and gives the body back.
func (b *base) get(url string, query url.Values, headers map[string]string) ([]byte, int, error) {
	if len(query) > 0 {
		url += "?" + query.Encode()
	}
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return b.do(request)
}

// post asks the service with a body.
func (b *base) post(url string, body []byte, headers map[string]string) ([]byte, int, error) {
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return b.do(request)
}

// put asks the service to change something.
func (b *base) put(url string, body []byte, headers map[string]string) ([]byte, int, error) {
	request, err := http.NewRequest(http.MethodPut, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return b.do(request)
}

// delete asks the service to remove something.
func (b *base) delete(url string, headers map[string]string) ([]byte, int, error) {
	request, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, 0, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return b.do(request)
}

// do sends a request and reads the answer.
func (b *base) do(request *http.Request) ([]byte, int, error) {
	response, err := b.client.Do(request)
	if err != nil {
		return nil, 0, dnsError("%v", err)
	}
	defer response.Body.Close()
	body, err := readAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, dnsError("%v", err)
	}
	return body, response.StatusCode, nil
}
