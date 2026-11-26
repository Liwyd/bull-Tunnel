package dns

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The clients are tested against a server that is right here: the same
// requests, the same bodies and the same answers as the Python panel.

// aProvider is a DNS account of the tests.
func aProvider(kind string, token string, zone string) *model.DNSProvider {
	return &model.DNSProvider{Name: kind, Kind: kind, APIToken: token, Zone: zone, ID: "p1"}
}

// aClient is a client of the tests, with the address of the test server.
func aClient(t *testing.T, provider *model.DNSProvider, server *httptest.Server) Client {
	t.Helper()
	client, err := New(provider, "")
	if err != nil {
		t.Fatalf("the client cannot be built: %v", err)
	}
	switch c := client.(type) {
	case *cloudflare:
		c.apiBase = server.URL
	case *arvan:
		c.apiBase = server.URL
	}
	return client
}

// TestCloudflareUpdatesAndRemovesTheDuplicates: one A record per name, or
// a stale record would keep sending half the users to the dead server.
func TestCloudflareUpdatesAndRemovesTheDuplicates(t *testing.T) {
	records := []map[string]any{
		{"id": "r1", "name": "a.example.com", "content": "1.1.1.1"},
		{"id": "r2", "name": "a.example.com", "content": "2.2.2.2"},
	}
	type call struct {
		method string
		path   string
		body   map[string]any
	}
	var seen []call

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&body)
		}
		seen = append(seen, call{method: r.Method, path: r.URL.Path, body: body})
		if r.Header.Get("authorization") != "Bearer tok" {
			t.Errorf("the token is %q, it must be the one of the account", r.Header.Get("authorization"))
		}
		write := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
		}
		switch {
		case r.URL.Path == "/zones":
			write([]any{map[string]any{"id": "z1"}})
		case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodGet:
			name := r.URL.Query().Get("name")
			out := []any{}
			for _, record := range records {
				if record["name"] == name {
					out = append(out, record)
				}
			}
			write(out)
		default:
			write(map[string]any{})
		}
	}))
	defer server.Close()

	client := aClient(t, aProvider("cloudflare", "tok", "example.com"), server)
	addresses, err := client.GetA("a.example.com")
	if err != nil {
		t.Fatalf("the addresses cannot be read: %v", err)
	}
	if len(addresses) != 2 || addresses[0] != "1.1.1.1" || addresses[1] != "2.2.2.2" {
		t.Errorf("the addresses are %v, they must be the two of the file", addresses)
	}

	if err := client.SetA("a.example.com", "9.9.9.9", 60); err != nil {
		t.Fatalf("the record cannot be written: %v", err)
	}
	puts := []call{}
	deletes := []call{}
	for _, c := range seen {
		switch c.method {
		case http.MethodPut:
			puts = append(puts, c)
		case http.MethodDelete:
			deletes = append(deletes, c)
		}
	}
	if len(puts) != 1 || !strings.HasSuffix(puts[0].path, "/dns_records/r1") {
		t.Fatalf("the record was not changed: %v", puts)
	}
	if puts[0].body["content"] != "9.9.9.9" || puts[0].body["ttl"] != float64(60) || puts[0].body["proxied"] != false {
		t.Errorf("the record is %v, it must be the one the panel writes", puts[0].body)
	}
	if puts[0].body["type"] != "A" || puts[0].body["name"] != "a.example.com" {
		t.Errorf("the record is %v, it must name the record", puts[0].body)
	}
	if len(deletes) != 1 || !strings.HasSuffix(deletes[0].path, "/dns_records/r2") {
		t.Errorf("the second record was not removed: %v", deletes)
	}
}

// TestCloudflareCreatesTheMissingRecordAndSaysWhatIsWrong: a record that is
// not there is created, and a refusal is said in the panel's words.
func TestCloudflareCreatesTheMissingRecordAndSaysWhatIsWrong(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones" {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{map[string]any{"id": "z1"}}})
			return
		}
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
	}))
	defer server.Close()

	client := aClient(t, aProvider("cloudflare", "tok", "example.com"), server)
	if err := client.SetA("new.example.com", "9.9.9.9", 60); err != nil {
		t.Fatalf("the record cannot be written: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"message": "Authentication error"}}})
	}))
	defer bad.Close()
	client = aClient(t, aProvider("cloudflare", "tok", "example.com"), bad)
	err := client.SetA("a.example.com", "1.1.1.1", 60)
	if err == nil {
		t.Fatal("the panel says the record was written, it must not")
	}
	if !strings.Contains(err.Error(), "Authentication error") {
		t.Errorf("the panel said %q, it must say what the service said", err)
	}
}

// TestTheZoneIsChecked: a record outside the zone is refused before it is
// written.
func TestTheZoneIsChecked(t *testing.T) {
	client, err := New(aProvider("cloudflare", "tok", "example.com"), "")
	if err != nil {
		t.Fatalf("the client cannot be built: %v", err)
	}
	c := client.(*cloudflare)
	if name, err := c.relative("example.com"); err != nil || name != "@" {
		t.Errorf("the name of the apex is %q, it must be @", name)
	}
	if name, err := c.relative("a.b.example.com."); err != nil || name != "a.b" {
		t.Errorf("the name is %q, it must be a.b", name)
	}
	if _, err := c.relative("evil.com"); err == nil {
		t.Error("the panel accepted a name outside the zone, it must not")
	}
}

// TestArvanUpdatesTheExistingRecord: the record of ArvanCloud has a list of
// addresses, and its minimum time is two minutes.
func TestArvanUpdatesTheExistingRecord(t *testing.T) {
	type call struct {
		method string
		path   string
		body   map[string]any
	}
	var seen []call

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&body)
		}
		seen = append(seen, call{method: r.Method, path: r.URL.Path, body: body})
		if r.Header.Get("authorization") != "Apikey secret" {
			t.Errorf("the key is %q, it must be the one of the account", r.Header.Get("authorization"))
		}
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("search") != "vpn" {
				t.Errorf("the search is %q, it must be the name of the record", r.URL.Query().Get("search"))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": []any{
					map[string]any{"id": "x1", "type": "a", "name": "vpn", "value": []any{map[string]any{"ip": "1.1.1.1"}}},
					map[string]any{"id": "x2", "type": "cname", "name": "vpn2", "value": map[string]any{"host": "a"}},
				},
				"meta": map[string]any{"last_page": 1},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer server.Close()

	client := aClient(t, aProvider("arvan", "secret", "example.ir"), server)
	addresses, err := client.GetA("vpn.example.ir")
	if err != nil {
		t.Fatalf("the addresses cannot be read: %v", err)
	}
	if len(addresses) != 1 || addresses[0] != "1.1.1.1" {
		t.Errorf("the addresses are %v, they must be the one of the file", addresses)
	}

	if err := client.SetA("vpn.example.ir", "5.5.5.5", 60); err != nil {
		t.Fatalf("the record cannot be written: %v", err)
	}
	puts := []call{}
	for _, c := range seen {
		if c.method == http.MethodPut {
			puts = append(puts, c)
		}
	}
	if len(puts) != 1 {
		t.Fatalf("the record was not changed: %v", seen)
	}
	if !strings.HasSuffix(puts[0].path, "/domains/example.ir/dns-records/x1") {
		t.Errorf("the record was changed at %q, it must be the one of the file", puts[0].path)
	}
	value, _ := puts[0].body["value"].([]any)
	if len(value) != 1 {
		t.Fatalf("the value is %v, it must be a list with one address", puts[0].body["value"])
	}
	entry, _ := value[0].(map[string]any)
	if entry["ip"] != "5.5.5.5" {
		t.Errorf("the address is %v, it must be 5.5.5.5", entry["ip"])
	}
	if puts[0].body["type"] != "a" || puts[0].body["name"] != "vpn" {
		t.Errorf("the record is %v, it must name the record", puts[0].body)
	}
	if puts[0].body["ttl"] != float64(120) {
		t.Errorf("the time of the record is %v, it must be 120, the minimum of ArvanCloud", puts[0].body["ttl"])
	}
}

// TestArvanCreatesTheRecordOfTheApex: the name of the apex is "@".
func TestArvanCreatesTheRecordOfTheApex(t *testing.T) {
	type call struct {
		method string
		body   map[string]any
	}
	var seen []call

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&body)
		}
		seen = append(seen, call{method: r.Method, body: body})
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "meta": map[string]any{"last_page": 1}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer server.Close()

	client := aClient(t, aProvider("arvan", "Apikey k", "example.ir"), server)
	if err := client.SetA("example.ir", "5.5.5.5", 60); err != nil {
		t.Fatalf("the record cannot be written: %v", err)
	}
	posts := []call{}
	for _, c := range seen {
		if c.method == http.MethodPost {
			posts = append(posts, c)
		}
	}
	if len(posts) != 1 {
		t.Fatalf("the record was not created: %v", seen)
	}
	if posts[0].body["name"] != "@" {
		t.Errorf("the name is %v, it must be @", posts[0].body["name"])
	}
}

// TestTheTextOfATXTRecord: the quotes inside the text are escaped, the way
// the command channel needs it.
func TestTheTextOfATXTRecord(t *testing.T) {
	quoted := QuoteTXT(`{"id":"e1","action":"run"}`)
	if quoted != `"{\"id\":\"e1\",\"action\":\"run\"}"` {
		t.Errorf("the text is quoted %s, the quotes inside must be escaped", quoted)
	}
	if UnquoteTXT(quoted) != `{"id":"e1","action":"run"}` {
		t.Errorf("the text is read back as %s, it must be the one that went in", UnquoteTXT(quoted))
	}
	if QuoteTXT(`a\b`) != `"a\\b"` {
		t.Errorf("the text is quoted %s, the backslash must be escaped", QuoteTXT(`a\b`))
	}
}

// aSocksServer is a SOCKS5 server for the tests: it asks for the login when
// one is given, remembers where it was asked to connect, and passes the bytes
// on. It gives back its address.
func aSocksServer(t *testing.T, user string, password string) (string, func() []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	var mu sync.Mutex
	asked := []string{}
	serve := func(conn net.Conn) {
		defer conn.Close()
		head := make([]byte, 2)
		if _, err := io.ReadFull(conn, head); err != nil || head[0] != 5 {
			return
		}
		methods := make([]byte, head[1])
		if _, err := io.ReadFull(conn, methods); err != nil {
			return
		}
		if user != "" {
			conn.Write([]byte{5, 2})
			sub := make([]byte, 2)
			if _, err := io.ReadFull(conn, sub); err != nil {
				return
			}
			given := make([]byte, int(sub[1])+1)
			if _, err := io.ReadFull(conn, given); err != nil {
				return
			}
			name := string(given[:sub[1]])
			secret := make([]byte, given[sub[1]])
			if _, err := io.ReadFull(conn, secret); err != nil {
				return
			}
			if name != user || string(secret) != password {
				conn.Write([]byte{1, 1})
				return
			}
			conn.Write([]byte{1, 0})
		} else {
			conn.Write([]byte{5, 0})
		}
		request := make([]byte, 4)
		if _, err := io.ReadFull(conn, request); err != nil {
			return
		}
		var host string
		switch request[3] {
		case 1:
			ip := make([]byte, 4)
			io.ReadFull(conn, ip)
			host = net.IP(ip).String()
		case 3:
			size := make([]byte, 1)
			io.ReadFull(conn, size)
			name := make([]byte, size[0])
			io.ReadFull(conn, name)
			host = string(name)
		case 4:
			ip := make([]byte, 16)
			io.ReadFull(conn, ip)
			host = net.IP(ip).String()
		}
		port := make([]byte, 2)
		io.ReadFull(conn, port)
		target := net.JoinHostPort(host, strconv.Itoa(int(port[0])<<8|int(port[1])))
		mu.Lock()
		asked = append(asked, target)
		mu.Unlock()
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		defer upstream.Close()
		conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		go io.Copy(upstream, conn)
		io.Copy(conn, upstream)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serve(conn)
		}
	}()
	return listener.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, asked...)
	}
}

// TestASocksProxyIsDialledByItsAddress: BT_DNS_PROXY=socks5://host:port. The
// dialer used to be given the whole URL as if it were an address, so every
// request through a SOCKS5 proxy failed before it left the machine.
func TestASocksProxyIsDialledByItsAddress(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("through the proxy"))
	}))
	defer backend.Close()

	cases := []struct {
		name     string
		user     string
		password string
		url      func(address string) string
	}{
		{"without a login", "", "", func(a string) string { return "socks5://" + a }},
		{"with a login", "bull", "s3cret", func(a string) string { return "socks5://bull:s3cret@" + a }},
		{"remote resolution", "", "", func(a string) string { return "socks5h://" + a }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			address, asked := aSocksServer(t, c.user, c.password)
			client, err := httpClient(c.url(address))
			if err != nil {
				t.Fatalf("the client cannot be built: %v", err)
			}
			response, err := client.Get(backend.URL)
			if err != nil {
				t.Fatalf("the request through the proxy failed: %v", err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if string(body) != "through the proxy" {
				t.Errorf("the answer is %q, it must be the one of the backend", body)
			}
			want := strings.TrimPrefix(backend.URL, "http://")
			if got := asked(); len(got) != 1 || got[0] != want {
				t.Errorf("the proxy was asked for %v, it must be asked for %s", got, want)
			}
		})
	}

	// a login the proxy does not accept is an error, not a silent direct call
	address, _ := aSocksServer(t, "bull", "s3cret")
	client, err := httpClient("socks5://bull:wrong@" + address)
	if err != nil {
		t.Fatalf("the client cannot be built: %v", err)
	}
	if _, err := client.Get(backend.URL); err == nil {
		t.Error("the request went through a proxy that refused the login")
	}
}

// TestAnHTTPProxyIsUsedAsAProxy: the other schemes keep their meaning.
func TestAnHTTPProxyIsUsedAsAProxy(t *testing.T) {
	asked := make(chan string, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked <- r.URL.String()
		w.Write([]byte("proxied"))
	}))
	defer proxyServer.Close()
	client, err := httpClient(proxyServer.URL)
	if err != nil {
		t.Fatalf("the client cannot be built: %v", err)
	}
	response, err := client.Get("http://dns.example.com/zone")
	if err != nil {
		t.Fatalf("the request failed: %v", err)
	}
	response.Body.Close()
	if got := <-asked; got != "http://dns.example.com/zone" {
		t.Errorf("the proxy was asked for %q, it must be asked for the full address", got)
	}
}
