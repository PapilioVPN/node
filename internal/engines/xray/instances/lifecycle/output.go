package lifecycle

import (
	"bytes"

	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
)

// outputWriter frames a single child pipe. os/exec owns and joins the pipe
// copy goroutine. Oversized lines retain a bounded prefix while draining to LF.
type outputWriter struct {
	run       *live.Execution
	source    live.Source
	prefix    []byte
	truncated bool
}

func (w *outputWriter) Write(data []byte) (int, error) {
	size := len(data)
	for len(data) > 0 {
		line, rest, found := bytes.Cut(data, []byte{'\n'})
		if found && len(w.prefix) == 0 && !w.truncated {
			w.run.Publish(w.source, line[:min(len(line), live.MaxMessageBytes)], len(line) > live.MaxMessageBytes)
		} else {
			count := min(len(line), live.MaxMessageBytes-len(w.prefix))
			if count != 0 && w.prefix == nil {
				w.prefix = make([]byte, 0, live.MaxMessageBytes)
			}
			w.prefix = append(w.prefix, line[:count]...)
			w.truncated = w.truncated || count != len(line)
			if found {
				w.finish()
			}
		}
		data = rest
	}
	return size, nil
}

func (w *outputWriter) finish() {
	if len(w.prefix) != 0 || w.truncated {
		w.run.Publish(w.source, w.prefix, w.truncated)
		w.prefix = w.prefix[:0]
		w.truncated = false
	}
}
