package runconfig

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"syscall"
)

// Match quoted strings before comments so URLs and escaped quotes are untouched.
var configComments = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|//[^\n]*|#[^\n]*|/\*(?:[^*]|\*+[^*/])*\*+/`)

func hasOrdinaryObservatory(configFile string) bool {
	// Xray remains responsible for decoding and validating the full configuration.
	file, err := os.OpenFile(configFile, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	var features struct {
		Observatory *json.RawMessage `json:"observatory"`
	}
	if err := json.NewDecoder(file).Decode(&features); err == nil {
		return features.Observatory != nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return false
	}
	// Xray accepts Java- and Python-style comments in local JSON.
	content = configComments.ReplaceAllFunc(content, func(match []byte) []byte {
		if match[0] == '"' {
			return match
		}
		if match[0] == '/' && match[1] == '*' {
			return bytes.Repeat([]byte{'\n'}, bytes.Count(match, []byte{'\n'}))
		}
		return nil
	})
	features.Observatory = nil
	return json.NewDecoder(bytes.NewReader(content)).Decode(&features) == nil && features.Observatory != nil
}
