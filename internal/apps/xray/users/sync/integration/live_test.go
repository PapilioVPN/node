//go:build integration && linux

package integration_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

func traffic(address, target string, id int) error {
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	uuid, _ := hex.DecodeString(fmt.Sprintf("00000000000040008000%012d", id+1))
	host, port, _ := net.SplitHostPort(target)
	number, _ := strconv.Atoi(port)
	header := append([]byte{0}, uuid...)
	header = append(header, 0, 1)
	header = binary.BigEndian.AppendUint16(header, uint16(number))
	header = append(header, 1)
	header = append(header, net.ParseIP(host).To4()...)
	header = append(header, []byte("GET / HTTP/1.1\r\nHost: sync\r\nConnection: close\r\n\r\n")...)
	if _, err := conn.Write(header); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	var response [2]byte
	if _, err := io.ReadFull(reader, response[:]); err != nil {
		return err
	}
	if response != [2]byte{0, 0} {
		return fmt.Errorf("unexpected VLESS framing")
	}
	reply, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		return err
	}
	defer reply.Body.Close()
	body, err := io.ReadAll(io.LimitReader(reply.Body, 32))
	if err != nil || string(body) != "sync-ok" {
		return fmt.Errorf("payload failed: %v", err)
	}
	return nil
}
func TestNativeCallGrowthAuthReplayAndRevocation(t *testing.T) {
	for _, n := range []int{1, 50, 51, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := newFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			revision, err := f.service.Revision(ctx, "one")
			if err != nil {
				t.Fatal(err)
			}
			r := domain.Request{InstanceID: "one", OperationID: "add", Expected: revision, Mode: domain.Delta, Selection: []string{"selected"}, Sets: []accounts.InboundUserSet{{InboundTag: "selected", Accounts: [][]byte{}}}}
			for i := 0; i < n; i++ {
				r.Sets[0].Accounts = append(r.Sets[0].Accounts, nativeUser(i))
			}
			f.calls.take()
			plan, err := f.service.Preview(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			previewCalls := f.calls.take()
			if previewCalls["ListInbounds"] != 1 || previewCalls["GetInboundUsers"] != 1 || len(previewCalls) != 2 {
				t.Fatalf("preview native calls: %v", previewCalls)
			}
			receipt, err := f.service.Apply(ctx, "one", plan.PlanID, plan.Revision)
			if err != nil || receipt.Receipt.Effect != accounts.Completed {
				t.Fatal(receipt, err)
			}
			applyCalls := f.calls.take()
			if applyCalls["AlterInbound"] != n || applyCalls["ListInbounds"] != 2 || applyCalls["GetInboundUsers"] != 1 {
				t.Fatalf("apply calls: %v", applyCalls)
			}
			if _, err := f.service.Apply(ctx, "one", plan.PlanID, plan.Revision); err != nil {
				t.Fatal(err)
			}
			if calls := f.calls.take(); len(calls) != 0 {
				t.Fatalf("replay called native: %v", calls)
			}
			if err := traffic(f.address, f.target, 0); err != nil {
				t.Fatal("authentication", err)
			}
			r.OperationID = "unchanged"
			r.Expected, _ = f.service.Revision(ctx, "one")
			plan, err = f.service.Preview(ctx, r)
			if err != nil || plan.Differences[0].Unchanged != n {
				t.Fatal(plan, err)
			}
			if _, err = f.service.Apply(ctx, "one", plan.PlanID, plan.Revision); err != nil {
				t.Fatal(err)
			}
			unchangedCalls := f.calls.take()
			if unchangedCalls["AlterInbound"] != 0 {
				t.Fatal(unchangedCalls)
			}
			r.OperationID = "remove"
			r.Expected, _ = f.service.Revision(ctx, "one")
			r.Sets = nil
			r.Removals = []accounts.Removal{{InboundTag: "selected", Email: "member-0"}}
			plan, err = f.service.Preview(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.service.Apply(ctx, "one", plan.PlanID, plan.Revision); err != nil {
				t.Fatal(err)
			}
			if err := traffic(f.address, f.target, 0); err == nil {
				t.Fatal("removed credential still authenticates")
			}
			t.Logf("native n=%d preview=%v apply=%v replay=0 unchanged=%v", n, previewCalls, applyCalls, unchangedCalls)
		})
	}
}
