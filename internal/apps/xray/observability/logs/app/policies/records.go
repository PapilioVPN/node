// Package policies projects untrusted console text to a closed safe vocabulary.
package policies

import "bytes"

// Project never echoes a substring of native output. Redacting individual token
// patterns cannot protect arbitrary config/credential error text, including
// split and encoded material. Unknown messages therefore lose their details too.
func Project(raw []byte) (level, kind, message string) {
	level = "unknown"
	for _, candidate := range []struct{ marker, name string }{{"[Debug]", "debug"}, {"[Info]", "info"}, {"[Warning]", "warning"}, {"[Error]", "error"}} {
		if bytes.Contains(raw, []byte(candidate.marker)) {
			level = candidate.name
			break
		}
	}
	kind, message = "diagnostic", "Xray diagnostic; details redacted"
	switch {
	case bytes.Contains(raw, []byte("core: Xray ")) && bytes.Contains(raw, []byte(" started")):
		kind, message = "lifecycle", "Xray started; details redacted"
	case bytes.Contains(raw, []byte(" accepted ")):
		kind, message = "access", "Xray accepted a connection; details redacted"
	case bytes.Contains(raw, []byte(" rejected ")):
		kind, message = "access", "Xray rejected a connection; details redacted"
	}
	return
}
