package native

import (
	"context"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

const statusHeader = "HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\tData Channel Cipher"

func (p *process) Sessions(ctx context.Context) (lifecycle.SessionReport, error) {
	lines, err := p.control.request(ctx, "status 3", true)
	if err != nil {
		return lifecycle.SessionReport{}, err
	}
	return parseStatus(lines, p.control.id)
}

func (p *process) ExecutionID() string { return p.control.id }

func (p *process) Disconnect(ctx context.Context, sessionID uint64) error {
	_, err := p.control.request(ctx, "client-kill "+strconv.FormatUint(sessionID, 10), false)
	return err
}

func (p *process) Logs(ctx context.Context, after uint64) (lifecycle.LogBatch, error) {
	return p.control.logs(ctx, after)
}

func parseStatus(lines []string, id string) (lifecycle.SessionReport, error) {
	report := lifecycle.SessionReport{ExecutionID: id, Sessions: []lifecycle.Session{}}
	var header bool
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "TIME\t"):
			_, raw, _ := strings.Cut(line[5:], "\t")
			stamp, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return lifecycle.SessionReport{}, lifecycle.ErrManagement
			}
			report.ObservedAt = stamp
		case line == statusHeader || strings.HasPrefix(line, statusHeader+"\t"):
			header = true
		case strings.HasPrefix(line, "CLIENT_LIST\t"):
			fields := strings.SplitN(line, "\t", 14)
			if !header || len(fields) < 13 {
				return lifecycle.SessionReport{}, lifecycle.ErrManagement
			}
			rx, erx := strconv.ParseUint(fields[5], 10, 64)
			tx, etx := strconv.ParseUint(fields[6], 10, 64)
			connected, etime := strconv.ParseInt(fields[8], 10, 64)
			cid, eid := strconv.ParseUint(fields[10], 10, 64)
			if erx != nil || etx != nil || etime != nil || eid != nil {
				return lifecycle.SessionReport{}, lifecycle.ErrManagement
			}
			report.Sessions = append(report.Sessions, lifecycle.Session{
				ID: cid, CommonName: fields[1], RemoteAddress: fields[2],
				TunnelIPv4: fields[3], TunnelIPv6: fields[4],
				RXBytes: rx, TXBytes: tx, ConnectedAt: connected, Cipher: fields[12],
			})
		}
	}
	if !header || report.ObservedAt == 0 {
		return lifecycle.SessionReport{}, lifecycle.ErrManagement
	}
	return report, nil
}
