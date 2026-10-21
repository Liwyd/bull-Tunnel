package model

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Ordered is a map that keeps the order of its keys.
//
// The order is not a detail: the configuration the panel writes for a tunnel
// must be the same as the one the Python panel writes, key by key, and the
// settings of an adopted tunnel come from a file the panel read — in the
// order they are written in it.
//
// A Go map has no order of its own, so the keys are kept beside it, the way
// a Python dictionary keeps the order of its keys.
type Ordered struct {
	keys   []string
	values map[string]any
}

// NewOrdered returns an empty ordered map.
func NewOrdered() Ordered {
	return Ordered{values: map[string]any{}}
}

// OrderedDefaults are the values of the fields a state file does not mention.
func OrderedDefaults() Ordered { return NewOrdered() }

// Get returns the value of a key, and whether the key is there.
func (o *Ordered) Get(key string) (any, bool) {
	if o == nil || o.values == nil {
		return nil, false
	}
	value, there := o.values[key]
	return value, there
}

// Set writes a value. A key that is already there keeps its place.
func (o *Ordered) Set(key string, value any) {
	if o.values == nil {
		o.values = map[string]any{}
	}
	if _, there := o.values[key]; !there {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// Has tells whether the map holds a key.
func (o *Ordered) Has(key string) bool {
	_, there := o.Get(key)
	return there
}

// Keys are the keys of the map, in the order they came in.
func (o *Ordered) Keys() []string {
	if o == nil {
		return nil
	}
	return append([]string{}, o.keys...)
}

// Len is how many keys the map holds.
func (o *Ordered) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Clone is a copy of the map: the keys keep their order.
func (o *Ordered) Clone() Ordered {
	out := NewOrdered()
	for _, key := range o.Keys() {
		value, _ := o.Get(key)
		out.Set(key, value)
	}
	return out
}

// UnmarshalJSON reads an object and keeps the order of its keys.
func (o *Ordered) UnmarshalJSON(data []byte) error {
	o.keys = nil
	o.values = map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return err
	}
	if value == nil {
		return nil
	}
	object, ok := value.(Ordered)
	if !ok {
		return fmt.Errorf("expected an object")
	}
	o.keys = object.keys
	o.values = object.values
	return nil
}

// MarshalJSON writes the object with its keys in the order they came in.
func (o Ordered) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		value, err := json.Marshal(o.values[key])
		if err != nil {
			return nil, err
		}
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// decodeValue reads one value of a JSON object, keeping the order of the
// keys of the objects inside it.
//
// The numbers are read the way Python reads them: a whole number is an
// integer and the rest is a float. A port written as 443 must stay 443 and
// not become 443.0, which the engine would refuse.
func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			object := NewOrdered()
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("expected a key")
				}
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				object.Set(key, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			list := []any{}
			for decoder.More() {
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return list, nil
		}
		return nil, fmt.Errorf("unexpected %v", t)
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, nil
		}
		f, err := t.Float64()
		return f, err
	default:
		return token, nil
	}
}
