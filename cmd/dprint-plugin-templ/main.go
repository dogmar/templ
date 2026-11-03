//go:build wasip1

// Command dprint-plugin-templ is a dprint Wasm plugin (schema version 4)
// that formats templ files. Build it with build.sh; it is a wasip1 reactor,
// so main is never called by dprint.
package main

import (
	"encoding/json"

	"github.com/a-h/templ/internal/format"
)

func main() {}

const licenseText = "MIT License. See https://github.com/a-h/templ/blob/main/LICENSE"

// config is a configuration dprint registered: the plugin's section of the
// dprint configuration file and the global configuration.
type config struct {
	Plugin map[string]any `json:"plugin"`
	Global map[string]any `json:"global"`
}

var (
	configs        = map[uint32]config{}
	overrideConfig []byte
	filePath       string
	formattedText  []byte
	errorText      string
)

func storeConfig(configID uint32, raw []byte) {
	var c config
	_ = json.Unmarshal(raw, &c)
	configs[configID] = c
}

func configDiagnostics(configID uint32) []byte { return []byte("[]") }

func resolvedConfig(configID uint32) []byte { return []byte("{}") }

func pluginInfo() []byte {
	b, _ := json.Marshal(map[string]any{
		"name":                    "dprint-plugin-templ",
		"version":                 "0.0.0",
		"configKey":               "templ",
		"helpUrl":                 "https://templ.guide",
		"configSchemaUrl":         "",
		"supportsRangeFormatting": false,
	})
	return b
}

// Results of format, as dprint reads them.
const (
	noChange = 0
	changed  = 1
	failed   = 2
)

func formatFile(configID uint32, src []byte) int {
	overrideConfig = nil
	path := filePath
	filePath = ""
	out, didChange, err := format.Templ(src, path, format.Config{
		// A command that does not exist, so Prettier is never run.
		PrettierCommand: "templ-dprint-no-prettier",
	})
	if err != nil {
		errorText = err.Error()
		return failed
	}
	if !didChange {
		return noChange
	}
	formattedText = out
	return changed
}
