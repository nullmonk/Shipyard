package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"shipyard/pkg/shipfile"
)

// parseVarsValue resolves a --vars flag value to a key/value map.
//
// Accepted forms:
//   - inline JSON object: --vars '{"PORT":"8080"}'
//   - @-prefixed file path: --vars @vars.json (or --vars @/path/to/vars.json)
//   - bare file path: --vars vars.json (any value naming an existing file
//     is read as a file; otherwise it is parsed as inline JSON)
func parseVarsValue(raw string) (map[string]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var (
		data   []byte
		source string
	)
	switch {
	case strings.HasPrefix(trimmed, "@"):
		path := strings.TrimSpace(strings.TrimPrefix(trimmed, "@"))
		if path == "" {
			return nil, fmt.Errorf("--vars: missing file path after '@'")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("--vars: read file %s: %w", path, err)
		}
		data, source = b, path
	case isExistingFile(trimmed):
		b, err := os.ReadFile(trimmed)
		if err != nil {
			return nil, fmt.Errorf("--vars: read file %s: %w", trimmed, err)
		}
		data, source = b, trimmed
	default:
		data, source = []byte(trimmed), "flag value"
	}
	return parseVarsJSON(data, source)
}

func isExistingFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// parseVarsJSON decodes a JSON object into string overrides. String values
// are kept as-is; numbers and booleans are stringified. Anything else
// (null, arrays, nested objects) is rejected with a descriptive error.
func parseVarsJSON(data []byte, source string) (map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("--vars: invalid JSON in %s: %w", source, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("--vars: invalid JSON in %s: unexpected trailing data", source)
	}
	if raw == nil {
		return nil, fmt.Errorf("--vars: invalid JSON in %s: expected an object", source)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		s, err := stringifyVarValue(k, v)
		if err != nil {
			return nil, fmt.Errorf("--vars: %s: %w", source, err)
		}
		out[k] = s
	}
	return out, nil
}

func stringifyVarValue(key string, v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case json.Number:
		return string(t), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("variable %q must be a string, number, or boolean, got %s", key, jsonKind(v))
	}
}

func jsonKind(v any) string {
	if v == nil {
		return "null"
	}
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// parseVarAssign splits a --var KEY=VALUE assignment. The key is trimmed and
// must be non-empty; the value is kept verbatim (it may be empty or contain
// '=' and whitespace).
func parseVarAssign(s string) (string, string, error) {
	idx := strings.Index(s, "=")
	if idx < 0 {
		return "", "", fmt.Errorf("--var %q must be in KEY=VALUE form", s)
	}
	key := strings.TrimSpace(s[:idx])
	if key == "" {
		return "", "", fmt.Errorf("--var %q must be in KEY=VALUE form (empty key)", s)
	}
	return key, s[idx+1:], nil
}

// resolveVariableOverrides merges --vars (JSON object or file) with repeatable
// --var KEY=VALUE flags. Precedence: --var wins over --vars; among --var
// flags, later occurrences win.
//
// Unknown keys are warned about on stderr and dropped. This is deliberately
// lenient: the context layer (Shipfile.NewContext / VariablesMap) is strict
// and returns an error for undeclared variables, but the CLI filters those
// out first so a typo or extra key can't fail a build.
func resolveVariableOverrides(sf *shipfile.Shipfile, varsFlag string, varFlags []string) (map[string]string, error) {
	merged := make(map[string]string)
	if strings.TrimSpace(varsFlag) != "" {
		m, err := parseVarsValue(varsFlag)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			merged[k] = v
		}
	}
	for _, a := range varFlags {
		k, v, err := parseVarAssign(a)
		if err != nil {
			return nil, err
		}
		merged[k] = v
	}
	if len(merged) == 0 {
		return nil, nil
	}
	if sf != nil {
		known := make(map[string]bool, len(sf.Variables))
		for _, v := range sf.Variables {
			known[v.Name] = true
		}
		for k := range merged {
			if !known[k] {
				fmt.Fprintf(os.Stderr, "warning: unknown variable %q ignored (not declared in shipfile Variables)\n", k)
				delete(merged, k)
			}
		}
	}
	if len(merged) == 0 {
		return nil, nil
	}
	return merged, nil
}
