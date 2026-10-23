package main

import (
	"fmt"
	"strconv"
	"strings"
)

// menuItem is one line of the menu.
type menuItem struct {
	Title string
	Desc  string
	Do    func(*Panel, []string) (int, error)
}

// menuItems are what the menu offers.
var menuItems = []menuItem{
	{"Status", "the state of the panel in one screen", cmdStatus},
	{"Servers", "the servers the panel manages", cmdServers},
	{"Tunnels", "the tunnels between them", cmdTunnels},
	{"Domains", "the domains and their records", cmdDomains},
	{"DNS accounts", "the accounts the records are written with", cmdDNS},
	{"The tunnel engine", "the engine on the servers", cmdEngine},
	{"Backups", "make one, list them", cmdBackup},
	{"Check the installation", "is everything where it should be", cmdDoctor},
	{"The service", "start, stop, restart", cmdService},
	{"The log", "what the panel is doing", cmdLogs},
	{"The admin password", "set it", cmdPasswd},
	{"Quit", "leave", nil},
}

// menu is the menu: a list, a number, and it does the thing.
func menu() int {
	initOut()
	panel := newPanel()
	for {
		out.Title("bull-Tunnel")
		for i, item := range menuItems {
			fmt.Printf("  %s. %s %s\n", out.Bold(strconv.Itoa(i+1)), item.Title+strings.Repeat(" ", max(1, 24-len(item.Title))), out.Dim(item.Desc))
		}
		fmt.Println()
		answer, err := readLine()
		if err != nil {
			fmt.Println()
			return 0
		}
		switch strings.ToLower(answer) {
		case "q", "quit", "exit":
			return 0
		}
		number, err := strconv.Atoi(answer)
		if err != nil || number < 1 || number > len(menuItems) {
			out.Warn("that is not one of the numbers")
			continue
		}
		item := menuItems[number-1]
		if item.Do == nil {
			return 0
		}
		out.Title(item.Title)
		if _, err := item.Do(panel, []string{}); err != nil {
			out.Bad(err.Error())
		}
		fmt.Print("\n  press enter to go back ")
		readLine()
	}
}
