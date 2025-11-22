package coremain

import (
	"os"
	"strconv"
	"strings"
)

// applyPluginEnvOverrides inspects environment variables for keys with the
// prefix PLUGINS_. It supports keys in the form:
//
//	PLUGINS_<IDENT>_ARGS_<KEY_PATH>=<value>
//
// Where <IDENT> is a plugin tag (preferred) or plugin type, and <KEY_PATH>
// is underscore-separated path to fields under `args` (underscores map to
// nested keys). Example:
//
//	PLUGINS_ADD_GFWLIST_ARGS_PASSWD=secret
//	PLUGINS_MYTAG_ARGS_AUTO_RELOAD=true
//
// This function will locate the matching plugin (by Tag or Type) and set
// the corresponding values in the plugin's Args map.
func applyPluginEnvOverrides(cfg *Config) error {
	for _, e := range os.Environ() {
		// split into key and value
		kv := strings.SplitN(e, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := kv[0]
		val := kv[1]
		if !strings.HasPrefix(key, "PLUGINS_") {
			continue
		}

		// Remove PLUGINS_ prefix and split parts by underscore
		rest := strings.TrimPrefix(key, "PLUGINS_")
		parts := strings.Split(rest, "_")
		// Find the index of ARGS (case-insensitive)
		argIdx := -1
		for i, p := range parts {
			if strings.EqualFold(p, "ARGS") {
				argIdx = i
				break
			}
		}
		if argIdx <= 0 || argIdx == len(parts)-1 {
			// malformed or not a plugin-args env var
			continue
		}

		ident := strings.Join(parts[:argIdx], "_")
		keyParts := parts[argIdx+1:]
		// normalize key parts to lower-case
		for i := range keyParts {
			keyParts[i] = strings.ToLower(keyParts[i])
		}
		// two forms: nested (dots) and underscore-joined (original literal key)
		nestedKey := strings.Join(keyParts, ".")
		underscoreKey := strings.Join(keyParts, "_")

		// locate plugin by tag (preferred) or type
		for i := range cfg.Plugins {
			p := &cfg.Plugins[i]
			if strings.EqualFold(p.Tag, ident) || strings.EqualFold(p.Type, ident) {
				// ensure Args is a map[string]any
				var m map[string]any
				if p.Args == nil {
					m = make(map[string]any)
					p.Args = m
				} else {
					if mm, ok := p.Args.(map[string]any); ok {
						m = mm
					} else {
						// try to coerce other types to map via simple conversion
						// unsupported types are skipped
						continue
					}
				}

				// parse val into bool/int/float if possible
				parsed := parseStringValue(val)
				// set both nested and underscore forms to maximize compatibility
				setNestedMapValue(m, nestedKey, parsed)
				m[underscoreKey] = parsed
				// We process all envs; do not return early
			}
		}
	}
	return nil
}

// parseStringValue attempts to convert a string to bool/int/float, falling
// back to string when conversion is not possible.
func parseStringValue(s string) any {
	// booleans
	lower := strings.ToLower(s)
	if lower == "true" {
		return true
	}
	if lower == "false" {
		return false
	}
	// integer
	if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
		return iv
	}
	// float
	if fv, err := strconv.ParseFloat(s, 64); err == nil {
		return fv
	}
	// fallback to string
	return s
}

// setNestedMapValue sets a dotted key path (e.g. "a.b.c") into map m,
// creating intermediate maps as necessary.
func setNestedMapValue(m map[string]any, path string, val any) {
	if path == "" {
		return
	}
	parts := strings.Split(path, ".")
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = val
			return
		}
		// ensure intermediate map exists
		if next, ok := cur[p]; ok {
			if nm, ok2 := next.(map[string]any); ok2 {
				cur = nm
				continue
			}
			// type mismatch: overwrite with a new map
		}
		nm := make(map[string]any)
		cur[p] = nm
		cur = nm
	}
}
