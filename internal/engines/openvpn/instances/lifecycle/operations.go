package lifecycle

import (
	"context"
	"errors"
)

var (
	ErrNotReady         = errors.New("openvpn execution is not ready")
	ErrExecutionChanged = errors.New("openvpn execution changed")
	ErrSessionNotFound  = errors.New("openvpn session not found")
	ErrManagement       = errors.New("openvpn management is unavailable")
)

type Session struct {
	ID            uint64 `json:"id,string"`
	CommonName    string `json:"common_name"`
	RemoteAddress string `json:"remote_address"`
	TunnelIPv4    string `json:"tunnel_ipv4,omitempty"`
	TunnelIPv6    string `json:"tunnel_ipv6,omitempty"`
	RXBytes       uint64 `json:"rx_bytes,string"`
	TXBytes       uint64 `json:"tx_bytes,string"`
	ConnectedAt   int64  `json:"connected_at"`
	Cipher        string `json:"cipher,omitempty"`
}

type SessionReport struct {
	ExecutionID string    `json:"execution_id"`
	ObservedAt  int64     `json:"observed_at"`
	Sessions    []Session `json:"sessions"`
}

type Log struct {
	Sequence  uint64 `json:"sequence,string"`
	Timestamp int64  `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

type LogBatch struct {
	ExecutionID string `json:"execution_id"`
	Dropped     uint64 `json:"dropped,string,omitempty"`
	Entries     []Log  `json:"entries"`
}

func (s *Service) Sessions(ctx context.Context, id string) (SessionReport, error) {
	run, err := s.active(id, "")
	if err != nil {
		return SessionReport{}, err
	}
	return run.Sessions(ctx)
}

func (s *Service) Disconnect(ctx context.Context, id, executionID string, sessionID uint64) error {
	if executionID == "" {
		return ErrExecutionChanged
	}
	run, err := s.active(id, executionID)
	if err != nil {
		return err
	}
	return run.Disconnect(ctx, sessionID)
}

func (s *Service) FollowLogs(ctx context.Context, id, executionID string, after uint64, send func(LogBatch) error) error {
	run, err := s.active(id, executionID)
	if err != nil {
		return err
	}
	if after != 0 && executionID == "" {
		return ErrExecutionChanged
	}
	select {
	case s.logReaders <- struct{}{}:
		defer func() { <-s.logReaders }()
	default:
		return ErrBusy
	}
	for {
		batch, err := run.Logs(ctx, after)
		if errors.Is(err, ErrNotReady) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := send(batch); err != nil {
			return err
		}
		after = batch.Entries[len(batch.Entries)-1].Sequence
	}
}

func (s *Service) active(id, expected string) (Execution, error) {
	if !s.matches(id) {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.status.State != Ready || !running(s.run) {
		return nil, ErrNotReady
	}
	if expected != "" && expected != s.run.ExecutionID() {
		return nil, ErrExecutionChanged
	}
	return s.run, nil
}
