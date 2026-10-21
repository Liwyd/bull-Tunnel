package engine

import (
	"fmt"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/ssh"
)

// ufwAllow opens the ports on ufw if it is active. The mappings are reduced
// to their listening port (or range).
func ufwAllow(ports []string) string {
	numbers := []string{}
	for _, p := range ports {
		listen := strings.SplitN(p, "=", 2)[0]
		if strings.HasPrefix(listen, "127.") || strings.HasPrefix(listen, "[::1]") {
			continue
		}
		port := listen[strings.LastIndex(listen, ":")+1:] // "1.2.3.4:443" / "[::]:443" / "443"
		if strings.Contains(port, "-") {
			// ufw writes ranges as a:b and wants a protocol with them
			numbers = append(numbers, strings.ReplaceAll(port, "-", ":")+"/tcp", strings.ReplaceAll(port, "-", ":")+"/udp")
			continue
		}
		numbers = append(numbers, port)
	}
	if len(numbers) == 0 {
		return ""
	}
	rules := []string{}
	for _, n := range numbers {
		rules = append(rules, fmt.Sprintf("ufw allow %s >/dev/null 2>&1;", ssh.Quote(n)))
	}
	return fmt.Sprintf("if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q 'Status: active'; then %s fi\n",
		strings.Join(rules, " "))
}
