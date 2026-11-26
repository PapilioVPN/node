package native

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

const (
	replyLimit        = 4 << 20
	logCapacity       = 128
	managementTimeout = 2 * time.Second
)

type management struct {
	conn    *net.UnixConn
	id      string
	gate    chan struct{}
	replies chan string
	done    chan struct{}

	mu      sync.Mutex
	changed chan struct{}
	log     [logCapacity]lifecycle.Log
	last    uint64
}

func newManagement(conn *net.UnixConn, id string) *management {
	return &management{conn: conn, id: id, gate: make(chan struct{}, 1), replies: make(chan string, 32),
		done: make(chan struct{}), changed: make(chan struct{})}
}

func (m *management) read(lines *bufio.Scanner, exited <-chan struct{}) {
	defer close(m.done)
	defer m.conn.Close()
	for lines.Scan() {
		line := lines.Text()
		if m.notification(line) {
			// An internal restart can reuse client IDs within a process.
			// End this execution so the next Start has a fresh identity.
			if strings.HasPrefix(line, ">STATE:") && strings.Contains(line, ",RECONNECTING,") {
				return
			}
			continue
		}
		select {
		case m.replies <- line:
		case <-exited:
			return
		}
	}
}

func (m *management) notification(line string) bool {
	s, ok := strings.CutPrefix(line, ">LOG:")
	if !ok {
		return strings.HasPrefix(line, ">")
	}
	stamp, rest, ok := strings.Cut(s, ",")
	if !ok {
		return true
	}
	level, message, ok := strings.Cut(rest, ",")
	if !ok {
		return true
	}
	unix, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return true
	}
	if len(message) > 4096 {
		message = message[:4096]
	}
	message = strings.ToValidUTF8(message, "�")
	m.mu.Lock()
	m.last++
	m.log[(m.last-1)%logCapacity] = lifecycle.Log{Sequence: m.last, Timestamp: unix, Level: level, Message: message}
	close(m.changed)
	m.changed = make(chan struct{})
	m.mu.Unlock()
	return true
}

func (m *management) logs(ctx context.Context, after uint64) (lifecycle.LogBatch, error) {
	for {
		m.mu.Lock()
		last := m.last
		if after > last {
			m.mu.Unlock()
			return lifecycle.LogBatch{}, lifecycle.ErrExecutionChanged
		}
		if after < last {
			report := lifecycle.LogBatch{ExecutionID: m.id}
			if last-after > logCapacity {
				report.Dropped = last - after - logCapacity
				after = last - logCapacity
			}
			report.Entries = make([]lifecycle.Log, 0, last-after)
			for n := after; n < last; n++ {
				report.Entries = append(report.Entries, m.log[n%logCapacity])
			}
			m.mu.Unlock()
			return report, nil
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return lifecycle.LogBatch{}, ctx.Err()
		case <-m.done:
			return lifecycle.LogBatch{}, lifecycle.ErrNotReady
		}
	}
}

func (m *management) request(ctx context.Context, command string, multiline bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case m.gate <- struct{}{}:
		defer func() { <-m.gate }()
	default:
		return nil, lifecycle.ErrBusy
	}
	select {
	case <-m.done:
		return nil, lifecycle.ErrNotReady
	default:
	}
	if err := m.conn.SetWriteDeadline(time.Now().Add(managementTimeout)); err != nil {
		return nil, errors.Join(lifecycle.ErrManagement, err)
	}
	if _, err := io.WriteString(m.conn, command+"\n"); err != nil {
		_ = m.conn.Close()
		return nil, errors.Join(lifecycle.ErrManagement, err)
	}

	// Once dispatched, consume the complete response even if the caller
	// cancels. The next caller must never see this request's response.
	timer := time.NewTimer(managementTimeout)
	defer timer.Stop()
	var lines []string
	var size int
	var overflow bool
	for {
		select {
		case line := <-m.replies:
			if strings.HasPrefix(line, "ERROR:") {
				if multiline {
					return nil, lifecycle.ErrManagement
				}
				return nil, lifecycle.ErrSessionNotFound
			}
			if (multiline && line == "END") || (!multiline && strings.HasPrefix(line, "SUCCESS:")) {
				if overflow {
					return nil, lifecycle.ErrManagement
				}
				return lines, ctx.Err()
			}
			if overflow {
				continue
			}
			size += len(line) + 1
			if size > replyLimit {
				overflow = true
				continue
			}
			lines = append(lines, line)
		case <-timer.C:
			_ = m.conn.Close()
			return nil, lifecycle.ErrManagement
		case <-m.done:
			return nil, lifecycle.ErrManagement
		}
	}
}
