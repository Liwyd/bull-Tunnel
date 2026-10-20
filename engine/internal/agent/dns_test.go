package agent

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

// dnsServer is a tiny DNS server that answers every TXT question with one
// record. It is here so the test can prove that the agent really reads
// its commands over ordinary DNS.
type dnsServer struct {
	conn *net.UDPConn
	text string
}

func startDNSServer(t *testing.T, text string) (string, func()) {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	s := &dnsServer{conn: conn, text: text}
	go s.serve()
	return conn.LocalAddr().String(), func() { conn.Close() }
}

func (s *dnsServer) serve() {
	buf := make([]byte, 1500)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		reply, err := s.answer(buf[:n])
		if err != nil {
			continue
		}
		s.conn.WriteToUDP(reply, from)
	}
}

// answer builds the answer of a TXT question.
func (s *dnsServer) answer(query []byte) ([]byte, error) {
	if len(query) < 12 {
		return nil, errShort
	}
	// the question ends after the name and the two type fields
	off := 12
	for off < len(query) && query[off] != 0 {
		off += int(query[off]) + 1
	}
	off += 1
	if off+4 > len(query) {
		return nil, errShort
	}
	qend := off + 4
	qtype := binary.BigEndian.Uint16(query[off : off+2])

	out := make([]byte, 0, 512)
	out = append(out, query[0], query[1]) // the id
	out = append(out, 0x81, 0x80)         // the answer, recursion available
	out = append(out, 0x00, 0x01)         // one question
	out = append(out, 0x00, 0x01)         // one answer
	out = append(out, 0x00, 0x00, 0x00, 0x00)
	out = append(out, query[12:qend]...) // the question
	if qtype != 16 {                     // not TXT
		return out, nil
	}
	// the answer: the name as a pointer to the question
	out = append(out, 0xc0, 0x0c)
	out = append(out, 0x00, 0x10) // TXT
	out = append(out, 0x00, 0x01) // IN
	out = append(out, 0x00, 0x00, 0x00, 0x3c)
	rdata := make([]byte, 0, len(s.text)+2)
	rdata = append(rdata, byte(len(s.text)))
	rdata = append(rdata, s.text...)
	out = append(out, byte(len(rdata)>>8), byte(len(rdata)))
	out = append(out, rdata...)
	return out, nil
}

var errShort = &shortError{}

type shortError struct{}

func (e *shortError) Error() string { return "the question is too short" }

// withResolver points the resolver of the process at the test server.
func withResolver(t *testing.T, addr string) {
	t.Helper()
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", addr)
		},
	}
	t.Cleanup(func() { net.DefaultResolver = old })
}

// TestTheAgentReadsItsCommandOverRealDNS is the proof that the command
// channel works where nothing but DNS gets through.
func TestTheAgentReadsItsCommandOverRealDNS(t *testing.T) {
	cmd := Command{ID: "over-dns", Action: "status", TS: Now().Unix()}
	text := mustEncode(t, cmd)
	addr, stop := startDNSServer(t, text)
	defer stop()
	withResolver(t, addr)

	dir := t.TempDir()
	pub := &fakePublisher{}
	a := New(&Config{
		CommandRecord: "_bt-cmd.example.com",
		AnswerRecord:  "_bt-ans.example.com",
		Token:         "the-token",
		TunnelDir:     dir,
		Allowed:       []string{"status"},
	}, pub, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	texts, err := LookupTXT(ctx, "_bt-cmd.example.com")
	if err != nil {
		t.Fatalf("the command could not be read over dns: %v", err)
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "over-dns") {
		t.Fatalf("the dns server answered %v", texts)
	}
	a.poll(ctx)
	if len(pub.written) != 1 {
		t.Fatalf("the agent did not answer: %v", pub.written)
	}
	answer, err := DecodeAnswer(pub.written[0])
	if err != nil {
		t.Fatal(err)
	}
	if !answer.OK || answer.ID != "over-dns" {
		t.Errorf("the answer is wrong: %+v", answer)
	}
}
