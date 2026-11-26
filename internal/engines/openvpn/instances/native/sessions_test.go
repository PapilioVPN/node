package native

import "testing"

func TestStatus3ParsesFullWidthCountersAndSessionIdentity(t *testing.T) {
	lines := []string{
		"TITLE\tOpenVPN 2.6.19", "TIME\tdisplay\t1760000010", statusHeader,
		"CLIENT_LIST\talice\t192.0.2.2:11940\t10.0.0.2\t\t9007199254740993\t18446744073709551615\tdisplay\t1760000000\tUNDEF\t7\t17\tAES-256-GCM",
	}
	report, err := parseStatus(lines, "fixture-execution")
	if err != nil || len(report.Sessions) != 1 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	row := report.Sessions[0]
	if report.ExecutionID != "fixture-execution" || report.ObservedAt != 1760000010 || row.ID != 7 || row.RXBytes != 9007199254740993 || row.TXBytes != ^uint64(0) || row.RemoteAddress != "192.0.2.2:11940" {
		t.Fatalf("report = %+v, row = %+v", report, row)
	}
}

func TestStatus3RejectsMissingSchemaAndMalformedRows(t *testing.T) {
	for _, lines := range [][]string{
		{"TIME\tdisplay\t1760000010"},
		{"TIME\tdisplay\t1760000010", statusHeader, "CLIENT_LIST\tunframed"},
	} {
		if _, err := parseStatus(lines, "fixture"); err == nil {
			t.Fatalf("accepted invalid status: %q", lines)
		}
	}
}
