//go:build wasip1

package main

import "unsafe"

// shared is the buffer dprint and the plugin pass bytes through. dprint
// writes into it after clear_shared_bytes and reads from it after any export
// that returns a length. Go's garbage collector does not move objects, so a
// pointer to its backing array stays valid while shared holds it.
var shared = make([]byte, 0, 1)

// pointerTo returns the address of b's backing array in Wasm memory.
func pointerTo(b []byte) uint32 {
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b))))
}

// setShared stores b as the shared buffer and returns its length.
func setShared(b []byte) uint32 {
	if cap(b) == 0 {
		b = make([]byte, 0, 1)
	}
	shared = b
	return uint32(len(b))
}

// takeShared returns the shared buffer and empties it.
func takeShared() []byte {
	b := shared
	shared = make([]byte, 0, 1)
	return b
}

//go:wasmexport dprint_plugin_version_4
func dprintPluginVersion4() uint32 { return 4 }

//go:wasmexport get_shared_bytes_ptr
func getSharedBytesPtr() uint32 { return pointerTo(shared) }

//go:wasmexport clear_shared_bytes
func clearSharedBytes(size uint32) uint32 {
	shared = make([]byte, size, max(size, 1))
	return pointerTo(shared)
}

//go:wasmexport register_config
func registerConfig(configID uint32) { storeConfig(configID, takeShared()) }

//go:wasmexport release_config
func releaseConfig(configID uint32) { delete(configs, configID) }

//go:wasmexport get_config_diagnostics
func getConfigDiagnostics(configID uint32) uint32 {
	return setShared(configDiagnostics(configID))
}

//go:wasmexport get_resolved_config
func getResolvedConfig(configID uint32) uint32 {
	return setShared(resolvedConfig(configID))
}

//go:wasmexport get_config_file_matching
func getConfigFileMatching(configID uint32) uint32 {
	return setShared([]byte(`{"fileExtensions":["templ"],"fileNames":[],"additive":false}`))
}

//go:wasmexport get_license_text
func getLicenseText() uint32 { return setShared([]byte(licenseText)) }

//go:wasmexport get_plugin_info
func getPluginInfo() uint32 { return setShared(pluginInfo()) }

//go:wasmexport set_override_config
func setOverrideConfig() { overrideConfig = takeShared() }

//go:wasmexport set_file_path
func setFilePath() { filePath = string(takeShared()) }

//go:wasmexport format
func formatExport(configID uint32) uint32 {
	return uint32(formatFile(configID, takeShared()))
}

//go:wasmexport get_formatted_text
func getFormattedText() uint32 {
	b := formattedText
	formattedText = nil
	return setShared(b)
}

//go:wasmexport get_error_text
func getErrorText() uint32 {
	b := []byte(errorText)
	errorText = ""
	return setShared(b)
}
