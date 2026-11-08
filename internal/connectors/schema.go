package connectors

import (
	"encoding/json"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// Limits on what a connector may advertise. Everything a server sends is
// untrusted input.
const (
	maxToolName    = 64
	maxDescription = 1024
	maxSchemaBytes = 32 << 10
	maxProperties  = 64
	maxTools       = 256
)

var toolNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// normalizeSchema reduces an MCP input schema to the portable subset the
// agent loop validates: an object whose properties are strings, numbers,
// integers, booleans or arrays of those, and string "required" names. It
// returns an error for a schema it can't express faithfully, so the tool is
// listed as unavailable rather than offered with a looser schema.
func normalizeSchema(raw any) (map[string]any, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("schema doesn't encode: %w", err)
	}
	if len(b) > maxSchemaBytes {
		return nil, fmt.Errorf("schema is %d bytes (limit %d)", len(b), maxSchemaBytes)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil || s == nil {
		return nil, fmt.Errorf("schema is not a JSON object")
	}
	if t, _ := s["type"].(string); t != "object" {
		return nil, fmt.Errorf("schema type is %q, want object", s["type"])
	}
	props := map[string]any{}
	in, _ := s["properties"].(map[string]any)
	if len(in) > maxProperties {
		return nil, fmt.Errorf("%d properties (limit %d)", len(in), maxProperties)
	}
	for name, p := range in {
		if !toolNameRE.MatchString(name) {
			return nil, fmt.Errorf("property name %q isn't allowed", name)
		}
		ps, ok := p.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("property %q has no schema", name)
		}
		out, err := scalarOrArray(ps)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", name, err)
		}
		props[name] = out
	}
	norm := map[string]any{"type": "object", "properties": props}
	if req, ok := s["required"].([]any); ok && len(req) > 0 {
		names := make([]string, 0, len(req))
		for _, r := range req {
			n, ok := r.(string)
			if !ok {
				return nil, fmt.Errorf("required holds a non-string")
			}
			if _, known := props[n]; !known {
				return nil, fmt.Errorf("required names unknown property %q", n)
			}
			names = append(names, n)
		}
		norm["required"] = names
	}
	return norm, nil
}

func scalarOrArray(ps map[string]any) (map[string]any, error) {
	t, _ := ps["type"].(string)
	out := map[string]any{"type": t}
	if d, ok := ps["description"].(string); ok {
		out["description"] = clip(d, 300)
	}
	switch t {
	case "string", "number", "integer", "boolean":
		if e, ok := ps["enum"].([]any); ok && len(e) <= 64 {
			out["enum"] = e
		}
		return out, nil
	case "array":
		items, _ := ps["items"].(map[string]any)
		it, _ := items["type"].(string)
		switch it {
		case "string", "number", "integer", "boolean":
			out["items"] = map[string]any{"type": it}
			return out, nil
		}
		return nil, fmt.Errorf("arrays of %q aren't supported", it)
	}
	return nil, fmt.Errorf("type %q isn't supported", t)
}

// clip truncates s to n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
