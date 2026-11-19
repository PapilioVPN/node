package natsapi

import (
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
)

type summaryResponse struct {
	ID                   string  `json:"id"`
	UID                  uint32  `json:"uid"`
	User                 string  `json:"user"`
	Seat                 string  `json:"seat"`
	LeaderPID            *uint32 `json:"leader_pid"`
	Class                *string `json:"class"`
	Terminal             *string `json:"terminal"`
	Idle                 *bool   `json:"idle"`
	IdleSinceMonotonicUS *uint64 `json:"idle_since_monotonic_us,string"`
}

func summaryResult(row domain.Summary) summaryResponse {
	return summaryResponse{ID: row.ID, UID: row.UID, User: row.User, Seat: row.Seat, LeaderPID: row.LeaderPID, Class: row.Class, Terminal: row.Terminal, Idle: row.Idle, IdleSinceMonotonicUS: row.IdleSinceMonotonicUS}
}

type listResponse struct {
	Sessions   []summaryResponse `json:"sessions"`
	Extended   bool              `json:"extended"`
	NextOffset *uint32           `json:"next_offset"`
	ObservedAt time.Time         `json:"observed_at"`
	DurationNS int64             `json:"duration_ns,string"`
}

func listResult(result domain.ListObservation) listResponse {
	rows := make([]summaryResponse, 0, len(result.Page.Sessions))
	for _, row := range result.Page.Sessions {
		rows = append(rows, summaryResult(row))
	}
	return listResponse{Sessions: rows, Extended: result.Page.Extended, NextOffset: result.Page.NextOffset, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}

type detailsResponse struct {
	Summary            summaryResponse `json:"summary"`
	Remote             *bool           `json:"remote"`
	RemoteHost         *string         `json:"remote_host"`
	RemoteUser         *string         `json:"remote_user"`
	StartedUnixUS      *uint64         `json:"started_unix_us,string"`
	StartedMonotonicUS *uint64         `json:"started_monotonic_us,string"`
	Type               *string         `json:"type"`
	State              *string         `json:"state"`
	Active             *bool           `json:"active"`
}
type readResponse struct {
	Session    detailsResponse `json:"session"`
	ObservedAt time.Time       `json:"observed_at"`
	DurationNS int64           `json:"duration_ns,string"`
}

func readResult(result domain.ReadObservation) readResponse {
	row := result.Details
	return readResponse{Session: detailsResponse{Summary: summaryResult(row.Summary), Remote: row.Remote, RemoteHost: row.RemoteHost, RemoteUser: row.RemoteUser, StartedUnixUS: row.StartedUnixUS, StartedMonotonicUS: row.StartedMonotonicUS, Type: row.Type, State: row.State, Active: row.Active}, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}

type recordResponse struct {
	PID           int32  `json:"pid"`
	RecordID      string `json:"record_id"`
	User          string `json:"user"`
	Terminal      string `json:"terminal"`
	Host          string `json:"host"`
	StartedUnixUS int64  `json:"started_unix_us,string"`
	SessionID     int32  `json:"session_id"`
}
type recordsResponse struct {
	Records    []recordResponse `json:"records"`
	NextOffset *uint32          `json:"next_offset"`
	ObservedAt time.Time        `json:"observed_at"`
	DurationNS int64            `json:"duration_ns,string"`
}

func recordsResult(result domain.RecordsObservation) recordsResponse {
	rows := make([]recordResponse, 0, len(result.Page.Records))
	for _, row := range result.Page.Records {
		rows = append(rows, recordResponse{PID: row.PID, RecordID: row.RecordID, User: row.User, Terminal: row.Terminal, Host: row.Host, StartedUnixUS: row.StartedUnixUS, SessionID: row.SessionID})
	}
	return recordsResponse{Records: rows, NextOffset: result.Page.NextOffset, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}
