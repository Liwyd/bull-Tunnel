// Command bull-tunnel is the management tool of the panel.
//
// The panel does the work; this command is the hand on it. It runs on the
// machine the panel runs on, talks to the panel over its own API and needs
// no password: the panel wrote a key for it, in a file only the owner of
// the machine can read, and the panel accepts that key only from this
// machine.
//
// Run it without an argument and it opens a menu, the way BackPack's does:
//
//	bull-tunnel                 the menu
//	bull-tunnel status          the state of the panel in one screen
//	bull-tunnel servers         the servers: list, add, test, remove
//	bull-tunnel tunnels         the tunnels: list, build, rebuild, remove
//	bull-tunnel domains         the domains: list, add, switch, sync, remove
//	bull-tunnel dns             the DNS accounts: list, test, add, remove
//	bull-tunnel engine          the tunnel engine on the servers
//	bull-tunnel backup          make a backup, list them, restore one
//	bull-tunnel service         start, stop, restart, the status of the panel
//	bull-tunnel logs            the log of the panel
//	bull-tunnel passwd          set the admin password
//	bull-tunnel doctor          check the installation
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// version is the version of the panel. It is stamped at link time by
// build.sh.
var version = "1.1.3"

func main() {
	os.Exit(run(os.Args[1:]))
}

// run is the command itself, so that the tests can call it.
func run(args []string) int {
	if len(args) == 0 {
		return menu()
	}
	command := args[0]
	rest := args[1:]
	switch command {
	case "-h", "--help", "help":
		usage()
		return 0
	case "-v", "--version", "version":
		fmt.Println("bull-tunnel", version)
		return 0
	}
	handler, there := commands[command]
	if !there {
		out.Bad("unknown command: " + command)
		usage()
		return 2
	}
	panel := newPanel()
	code, err := handler(panel, rest)
	if err != nil {
		out.Bad(err.Error())
		return code
	}
	return code
}

// commands are what the tool can do.
var commands = map[string]func(*Panel, []string) (int, error){
	"status":  cmdStatus,
	"servers": cmdServers,
	"tunnels": cmdTunnels,
	"domains": cmdDomains,
	"dns":     cmdDNS,
	"engine":  cmdEngine,
	"backup":  cmdBackup,
	"service": cmdService,
	"logs":    cmdLogs,
	"passwd":  cmdPasswd,
	"doctor":  cmdDoctor,
}

// usage is what the tool says when it does not know what to do.
func usage() {
	fmt.Printf(`bull-tunnel %s — the management tool of the panel

  bull-tunnel                  the menu
  bull-tunnel status           the state of the panel in one screen
  bull-tunnel servers          list | add | test <id> | remove <id>
  bull-tunnel tunnels          list | build | rebuild | remove <id>
  bull-tunnel domains          list | add | switch <id> <server> | sync <id> | remove <id>
  bull-tunnel dns              list | test <id> | add | remove <id>
  bull-tunnel engine           the tunnel engine on the servers
  bull-tunnel backup           run | list | restore <name>
  bull-tunnel service          start | stop | restart | status
  bull-tunnel logs             the log of the panel
  bull-tunnel passwd           set the admin password
  bull-tunnel doctor           check the installation
`, version)
}

// ask asks the operator something.
func ask(prompt string, def string) string {
	if def != "" {
		prompt = fmt.Sprintf("%s [%s]", prompt, def)
	}
	fmt.Print(prompt + ": ")
	answer, err := readLine()
	if err != nil || answer == "" {
		return def
	}
	return answer
}

// confirm asks the operator a question that must be answered with yes.
func confirm(prompt string) bool {
	answer := strings.ToLower(strings.TrimSpace(ask(prompt+" (yes/no)", "no")))
	return answer == "yes" || answer == "y"
}

// plural is a number with the word that goes with it.
func plural(n int, one string, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	if many == "" {
		many = one + "s"
	}
	return fmt.Sprintf("%d %s", n, many)
}

// aNumber is a number of the arguments, or zero.
func aNumber(args []string, index int) int {
	if index >= len(args) {
		return 0
	}
	value, err := strconv.Atoi(args[index])
	if err != nil {
		return 0
	}
	return value
}
