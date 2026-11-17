package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	router "github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

var ErrInvalidRulePreparationSettings = errors.New("invalid Xray routing rule preparation settings")

// RoutingRulePreparationConfig bounds one conversion. Directory is the instance
// config directory; WorkDir already exists. Output includes the core.Config
// envelope and is bounded before decoding, not just the extracted routing bytes.
type RoutingRulePreparationConfig struct {
	Binary         string
	Directory      string
	WorkDir        string
	Timeout        time.Duration
	MaxInputBytes  int
	MaxOutputBytes int
	MaxConcurrent  int
}

// RoutingRulePreparer uses the instance's converter so asset lookups retain its
// executable directory and empty environment. No process-global SDK hooks change.
type RoutingRulePreparer struct {
	config RoutingRulePreparationConfig
	slots  chan struct{}
}

func NewRoutingRulePreparer(cfg RoutingRulePreparationConfig) (*RoutingRulePreparer, error) {
	if !filepath.IsAbs(cfg.Binary) || !filepath.IsAbs(cfg.Directory) || !filepath.IsAbs(cfg.WorkDir) || cfg.Timeout <= 0 || cfg.MaxInputBytes <= 0 || cfg.MaxOutputBytes <= 0 || cfg.MaxOutputBytes == math.MaxInt || cfg.MaxConcurrent <= 0 {
		return nil, ErrInvalidRulePreparationSettings
	}
	return &RoutingRulePreparer{config: cfg, slots: make(chan struct{}, cfg.MaxConcurrent)}, nil
}

// PrepareRoutingRule borrows one routing.rules[] JSON object and returns owned
// canonical router.Config bytes. It constructs no engine and applies no change.
func (p *RoutingRulePreparer) PrepareRoutingRule(ctx context.Context, content []byte) (result domain.PreparedRoutingRule, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(content) == 0 {
		return result, domain.ErrInvalidRoutingRule
	}
	if len(content) > p.config.MaxInputBytes {
		return result, domain.ErrRoutingRuleInputLimit
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return result, domain.ErrRoutingRulePreparationBusy
	}
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()
	candidate := struct {
		Routing struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"routing"`
	}{}
	candidate.Routing.Rules = []json.RawMessage{content}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return result, errors.Join(domain.ErrInvalidRoutingRule, err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	directory, err := os.MkdirTemp(p.config.WorkDir, "xray-rule-")
	if err != nil {
		return result, fmt.Errorf("create rule preparation directory: %w", err)
	}
	defer func() {
		cleanup := os.RemoveAll(directory)
		if cleanup != nil {
			cleanup = fmt.Errorf("remove rule preparation directory: %w", cleanup)
		}
		if cause := errors.Join(ctx.Err(), cleanup); cause != nil {
			err = errors.Join(err, cause)
			result = domain.PreparedRoutingRule{}
		}
	}()
	path := filepath.Join(directory, "routing.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return result, fmt.Errorf("write routing candidate: %w", err)
	}
	output, err := p.compile(ctx, path)
	if err != nil {
		return result, err
	}
	var compiled core.Config
	if err := proto.Unmarshal(output, &compiled); err != nil {
		return result, errors.Join(domain.ErrInvalidPreparedRoutingRule, err)
	}
	typeName := string(proto.MessageName(&router.Config{}))
	found := false
	for _, app := range compiled.App {
		if app.GetType() != typeName {
			continue
		}
		var routing router.Config
		if found || proto.Unmarshal(app.Value, &routing) != nil || len(routing.Rule) != 1 || routing.Rule[0] == nil {
			return domain.PreparedRoutingRule{}, domain.ErrInvalidPreparedRoutingRule
		}
		found = true
		result = domain.PreparedRoutingRule{Config: app.Value, RuleTag: routing.Rule[0].RuleTag}
	}
	if !found {
		return domain.PreparedRoutingRule{}, domain.ErrInvalidPreparedRoutingRule
	}
	return result, nil
}

func (p *RoutingRulePreparer) compile(ctx context.Context, path string) ([]byte, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create native routing output pipe: %w", err)
	}
	defer reader.Close()
	defer writer.Close()
	// ExtraFiles pipes are not managed by Cmd.WaitDelay. Closing our reader on
	// cancellation interrupts the read even if another writer still holds the fd.
	readCanceled := make(chan struct{})
	stopRead := context.AfterFunc(ctx, func() {
		_ = reader.Close()
		close(readCanceled)
	})
	defer func() {
		if !stopRead() {
			<-readCanceled
		}
	}()
	processContext, stop := context.WithCancel(ctx)
	defer stop()
	// The converter opens inherited fd 3 for protobuf only. Its stdout/stderr are
	// discarded; no diagnostic framing or unbounded output file needs parsing.
	command := exec.CommandContext(processContext, p.config.Binary, "convert", "pb", "-o", "/proc/self/fd/3", path)
	command.ExtraFiles = []*os.File{writer}
	command.Dir = p.config.Directory
	command.Env = []string{}
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	command.WaitDelay = p.config.Timeout
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start native routing conversion: %w", err)
	}
	_ = writer.Close()
	output, readError := io.ReadAll(io.LimitReader(reader, int64(p.config.MaxOutputBytes)+1))
	oversized := len(output) > p.config.MaxOutputBytes
	if readError != nil || oversized {
		stop()
		_ = reader.Close()
	}
	execution := command.Wait()
	if oversized {
		return nil, errors.Join(domain.ErrRoutingRuleOutputLimit, ctx.Err(), execution)
	}
	if cause := ctx.Err(); cause != nil {
		return nil, errors.Join(cause, readError, execution)
	}
	if readError != nil {
		return nil, fmt.Errorf("read native routing conversion: %w", errors.Join(readError, execution))
	}
	if execution != nil {
		var exit *exec.ExitError
		if errors.As(execution, &exit) && exit.ExitCode() == 1 {
			return nil, errors.Join(domain.ErrInvalidRoutingRule, execution)
		}
		return nil, fmt.Errorf("convert Xray routing rule: %w", execution)
	}
	return output, nil
}
