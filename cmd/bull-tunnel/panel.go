package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Where the panel is and how this command talks to it.
const (
	// Header carries the key the panel wrote for this tool.
	Header = "X-Bull-Tunnel-Key"
	// defaultBase is the panel of this machine.
	defaultBase = "http://127.0.0.1:8080"
)

// dataDir is where the panel keeps its files.
func dataDir() string {
	if dir := os.Getenv("BT_DATA_DIR"); dir != "" {
		return dir
	}
	return "/var/lib/bulltunnel"
}

// keyFile is the file with the key of this tool.
func keyFile() string {
	return filepath.Join(dataDir(), "management.key")
}

// base is the address of the panel.
func base() string {
	if url := os.Getenv("BT_PANEL_URL"); url != "" {
		return strings.TrimRight(url, "/")
	}
	return defaultBase
}

// Panel is the panel this command talks to.
type Panel struct {
	Base string
	Key  string
}

// newPanel reads the key of this tool.
func newPanel() *Panel {
	key := ""
	if content, err := os.ReadFile(keyFile()); err == nil {
		key = strings.TrimSpace(string(content))
	}
	return &Panel{Base: base(), Key: key}
}

// Error says that the panel refused or could not be asked.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func panelError(format string, args ...any) error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

// headers are the headers of every request.
func (p *Panel) headers() map[string]string {
	if p.Key == "" {
		return map[string]string{}
	}
	return map[string]string{Header: p.Key}
}

// call asks the panel something.
func (p *Panel) call(method string, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, p.Base+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range p.headers() {
		request.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, panelError("the panel does not answer at %s: %v", p.Base, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode == http.StatusUnauthorized {
		return nil, panelError("the panel refused the management key: is the service running as another user?")
	}
	if response.StatusCode >= 400 {
		detail := string(raw)
		var answer map[string]any
		if err := json.Unmarshal(raw, &answer); err == nil {
			if text, there := answer["detail"].(string); there {
				detail = text
			}
		}
		return nil, panelError("%s", detail)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return map[string]any{}, nil
		}
	}
	return out, nil
}

// state is the state of the panel.
func (p *Panel) state() (map[string]any, error) {
	return p.call(http.MethodGet, "/api/state", nil)
}

// ------------------------------------------------------------------ output

// Out is the output of the command: a colour when the terminal takes it.
type Out struct {
	Colour bool
}

var out = Out{}

// initOut decides whether the output has colours.
func initOut() {
	out.Colour = isTerminal(os.Stdout)
}

// isTerminal tells whether a file is a terminal.
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// colour wraps the text in a colour, when the terminal takes it.
func (o *Out) colour(text string, code string) string {
	if !o.Colour {
		return text
	}
	return "\033[" + code + "m" + text + "\033[0m"
}

// Bold is the text in bold.
func (o *Out) Bold(text string) string { return o.colour(text, "1") }

// Dim is the text quiet.
func (o *Out) Dim(text string) string { return o.colour(text, "2") }

// Green is the text in green.
func (o *Out) Green(text string) string { return o.colour(text, "32") }

// Red is the text in red.
func (o *Out) Red(text string) string { return o.colour(text, "31") }

// Yellow is the text in yellow.
func (o *Out) Yellow(text string) string { return o.colour(text, "33") }

// Blue is the text in blue.
func (o *Out) Blue(text string) string { return o.colour(text, "36") }

// Say writes a line.
func (o *Out) Say(text string) { fmt.Println(text) }

// OK writes a line that says it went well.
func (o *Out) OK(text string) { fmt.Println(o.Green("✔") + " " + text) }

// Bad writes a line that says it did not go well.
func (o *Out) Bad(text string) { fmt.Println(o.Red("✘") + " " + text) }

// Warn writes a line that says something is not right.
func (o *Out) Warn(text string) { fmt.Println(o.Yellow("!") + " " + text) }

// Info writes a line that says what is going on.
func (o *Out) Info(text string) { fmt.Println(o.Blue("›") + " " + text) }

// Title writes the title of a part.
func (o *Out) Title(text string) {
	fmt.Println()
	fmt.Println(o.Bold(text))
	fmt.Println(o.Dim(strings.Repeat("─", max(8, len(text)))))
}

// Table writes a table whose columns line up.
func (o *Out) Table(rows [][]string, head []string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(head))
	for i, cell := range head {
		widths[i] = len(cell)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	line := []string{}
	for i, cell := range head {
		line = append(line, strings.ToUpper(cell)+strings.Repeat(" ", widths[i]-len(cell)))
	}
	fmt.Println(o.Dim(strings.Join(line, "  ")))
	for _, row := range rows {
		line := []string{}
		for i, cell := range row {
			pad := 0
			if i < len(widths) {
				pad = widths[i] - len(cell)
			}
			line = append(line, cell+strings.Repeat(" ", pad))
		}
		fmt.Println(strings.Join(line, "  "))
	}
}

// Field writes a name and its value.
func (o *Out) Field(name string, value string) {
	fmt.Println("  " + o.Dim(name+strings.Repeat(" ", max(0, 18-len(name)))) + " " + value)
}

// mark is the state of something, with a colour.
func mark(state string) string {
	switch state {
	case "online", "up", "ok", "active":
		return out.Green("●")
	case "offline", "down", "broken", "failed":
		return out.Red("●")
	case "degraded", "testing":
		return out.Yellow("●")
	}
	return out.Dim("○")
}

// readLine reads one line of the operator's answer.
func readLine() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// max is the larger of two numbers.
func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
