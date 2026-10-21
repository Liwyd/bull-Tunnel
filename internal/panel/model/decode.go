package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// The decoding of the model is tolerant on purpose: the state file of a
// panel that is older than this one must still load, and the state file of
// a panel that is newer than this one must not break it either. The fields
// the file does not mention keep their default; the fields it does not know
// are ignored.
//
// The decoding of a model is therefore never done with json.Unmarshal
// directly: every structure here has its own UnmarshalJSON, which fills the
// defaults of the fields the file left out.

// UnmarshalJSON reads a server out of a state file.
func (s *Server) UnmarshalJSON(data []byte) error {
	return decode(data, s, ServerDefaults())
}

// UnmarshalJSON reads a tunnel out of a state file.
func (l *Link) UnmarshalJSON(data []byte) error {
	if err := decode(data, l, LinkDefaults()); err != nil {
		return err
	}
	l.Normalize()
	return nil
}

// UnmarshalJSON reads a DNS account out of a state file.
func (p *DNSProvider) UnmarshalJSON(data []byte) error {
	return decode(data, p, DNSProviderDefaults())
}

// UnmarshalJSON reads a domain out of a state file.
func (d *Domain) UnmarshalJSON(data []byte) error {
	return decode(data, d, DomainDefaults())
}

// UnmarshalJSON reads the settings out of a state file.
func (s *Settings) UnmarshalJSON(data []byte) error {
	return decode(data, s, SettingsDefaults())
}

// UnmarshalJSON reads an event out of a state file.
func (e *Event) UnmarshalJSON(data []byte) error {
	return decode(data, e, EventDefaults())
}

// UnmarshalJSON reads the outcome of a test out of a state file.
func (t *TestResult) UnmarshalJSON(data []byte) error {
	return decode(data, t, TestResultDefaults())
}

// decode fills a model from a JSON object: the fields the object mentions
// are read, the fields it leaves out keep the value they have in the
// defaults, and the fields it does not know are ignored.
func decode(data []byte, into any, defaults any) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	value := reflect.ValueOf(into).Elem()
	def := reflect.ValueOf(defaults)
	if def.Type() != value.Type() {
		return fmt.Errorf("the defaults are not of the type of the model")
	}
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		raw, present := keys[jsonName(field)]
		if !present {
			value.Field(i).Set(def.Field(i))
			continue
		}
		if err := json.Unmarshal(raw, value.Field(i).Addr().Interface()); err != nil {
			return fmt.Errorf("%s: %w", jsonName(field), err)
		}
	}
	return nil
}

// jsonName is the name a field has in the state file: the name in its tag,
// or the name of the field itself when it has no tag.
func jsonName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return field.Name
	}
	return name
}
