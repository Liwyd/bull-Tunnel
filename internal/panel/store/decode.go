package store

import (
	"encoding/json"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The state file is read with the tolerance the Python panel has: a field
// the file does not mention keeps its default, and a field the file does
// not know is ignored. These helpers read the lists and the maps of the
// file into the structures of the model.

// decodeList reads a list of models out of the state file. The second value
// it returns is the number of entries that were left out because they have
// no id: an entry without an id is not a server, and the panel says so
// rather than dying on it.
func decodeList[T any](raw map[string]any, key string) ([]*T, int) {
	out := []*T{}
	skipped := 0
	for _, entry := range sliceOf(raw[key]) {
		m, ok := entry.(map[string]any)
		if !ok {
			skipped++
			continue
		}
		var item T
		if !decodeInto(m, &item) {
			skipped++
			continue
		}
		if idOf(m) == "" {
			skipped++
			continue
		}
		out = append(out, &item)
	}
	return out, skipped
}

// decodeSettings reads the settings out of the state file.
func decodeSettings(m map[string]any) *model.Settings {
	var settings model.Settings
	if !decodeInto(m, &settings) {
		return defaultsSettings()
	}
	return &settings
}

// decodeEvents reads the log of the panel out of the state file.
func decodeEvents(raw map[string]any) []*model.Event {
	out := []*model.Event{}
	for _, entry := range sliceOf(raw["events"]) {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		var event model.Event
		if !decodeInto(m, &event) {
			continue
		}
		out = append(out, &event)
	}
	return out
}

// decodeStringMap reads a map of strings out of the state file.
func decodeStringMap(raw map[string]any, key string) map[string]string {
	out := map[string]string{}
	m, ok := raw[key].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// decodeLayout reads the canvas of the dashboard out of the state file.
func decodeLayout(raw map[string]any) map[string][]float64 {
	out := map[string][]float64{}
	m, ok := raw["layout"].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range m {
		list, ok := v.([]any)
		if !ok {
			continue
		}
		point := []float64{}
		for _, item := range list {
			if f, ok := item.(float64); ok {
				point = append(point, f)
			}
		}
		out[k] = point
	}
	return out
}

// decodeAnyMap reads a map of anything out of the state file.
func decodeAnyMap(raw map[string]any, key string) map[string]any {
	out := map[string]any{}
	m, ok := raw[key].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// decodeInto reads one model out of a part of the state file.
func decodeInto(m map[string]any, into any) bool {
	data, err := json.Marshal(m)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, into) == nil
}

// sliceOf is the list a field of the state file holds, or nothing.
func sliceOf(v any) []any {
	list, _ := v.([]any)
	return list
}

// idOf is the id of an entry of the state file.
func idOf(m map[string]any) string {
	id, _ := m["id"].(string)
	return id
}
