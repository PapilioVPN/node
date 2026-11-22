package store

import (
	"context"
	"errors"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"os"
	"testing"
)

func TestCorruptCandidatePreservesPrivateFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/private", []byte("private-state"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Stage(context.Background(), domain.Candidate{})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("candidate: %v", err)
	}
	data, err := os.ReadFile(dir + "/private")
	if err != nil || string(data) != "private-state" {
		t.Fatal("private state changed", err)
	}
}
