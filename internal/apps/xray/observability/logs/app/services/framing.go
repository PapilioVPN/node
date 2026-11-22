package services

import (
	"bytes"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
)

func frame(data []byte, pos position, final bool, limit int, tail bool) ([]domain.Record, []domain.Gap, position) {
	records := make([]domain.Record, 0, limit)
	gaps := []domain.Gap{}
	total := 0
	for len(data) > 0 && (tail || total < limit) {
		end := bytes.IndexByte(data, '\n')
		complete := end >= 0
		if !complete {
			end = len(data)
		}
		consumed := end
		if complete {
			consumed++
		}
		if pos.skipping {
			pos.offset += uint64(consumed)
			data = data[consumed:]
			pos.skipping = !complete
			continue
		}
		truncated := end > domain.MaxLineBytes
		if !complete && !final && !truncated {
			break
		}
		level, kind, message := policies.Project(data[:min(end, domain.MaxLineBytes)])
		r := domain.Record{StartOffset: pos.offset, NextOffset: pos.offset + uint64(consumed), Level: level, Kind: kind, Message: message, Redacted: true, Truncated: truncated || !complete, Partial: !complete}
		if len(records) < limit {
			records = append(records, r)
		} else {
			records[total%limit] = r
		}
		total++
		pos.offset += uint64(consumed)
		pos.skipping = !complete && !final
		data = data[consumed:]
	}
	if tail && total > limit {
		index := total % limit
		ordered := make([]domain.Record, 0, limit)
		ordered = append(ordered, records[index:]...)
		ordered = append(ordered, records[:index]...)
		records = ordered
		gaps = append(gaps, domain.Gap{Reason: "tail_omitted", FromOffset: 0, ToOffset: records[0].StartOffset, ExtentKnown: true})
	}
	for _, r := range records {
		if r.Truncated {
			gaps = append(gaps, domain.Gap{Reason: "record_truncated", FromOffset: r.StartOffset, ToOffset: r.NextOffset, ExtentKnown: !r.Partial})
		}
	}
	return records, gaps, pos
}
