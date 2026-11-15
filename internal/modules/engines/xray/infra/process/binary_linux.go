package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

var ErrInvalidInspectionSettings = errors.New("invalid Xray binary inspection settings")

type BinaryInspectionConfig struct {
	Binary         string
	Timeout        time.Duration
	MaxOutputBytes int
	MaxConcurrent  int
}

// BinaryInspector owns bounded, request-scoped native version executions. It has
// no persistent child, runtime/configuration access, cache or native API client.
type BinaryInspector struct {
	config BinaryInspectionConfig
	slots  chan struct{}
}

// NewBinaryInspector validates explicit limits without reading or executing files.
func NewBinaryInspector(config BinaryInspectionConfig) (*BinaryInspector, error) {
	if !filepath.IsAbs(config.Binary) || config.Timeout <= 0 || config.MaxOutputBytes <= 0 || config.MaxConcurrent <= 0 {
		return nil, ErrInvalidInspectionSettings
	}
	return &BinaryInspector{config: config, slots: make(chan struct{}, config.MaxConcurrent)}, nil
}

// ReadBinaryInfo runs exactly one native version command. Admission is immediate;
// saturated callers retain no queued work. Output storage is bounded before
// parsing, and native exec joins its child and output copier before releasing
// admission. OS process creation remains synchronous, as in lifecycle execution.
func (r *BinaryInspector) ReadBinaryInfo(ctx context.Context) (domain.BinaryInfo, error) {
	if err := ctx.Err(); err != nil {
		return domain.BinaryInfo{}, err
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		return domain.BinaryInfo{}, domain.ErrInspectionBusy
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	output := versionOutput{limit: r.config.MaxOutputBytes}
	command := exec.CommandContext(ctx, r.config.Binary, "version")
	command.Dir = filepath.Dir(r.config.Binary)
	command.Env = []string{}
	command.Stdout = &output
	command.WaitDelay = r.config.Timeout
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	err := command.Run()
	if output.exceeded {
		err = errors.Join(err, domain.ErrInfoOutputLimit)
	}
	if cause := ctx.Err(); cause != nil {
		return domain.BinaryInfo{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.BinaryInfo{}, fmt.Errorf("read Xray binary information: %w", err)
	}
	info, err := parseVersionOutput(output.data)
	if cause := ctx.Err(); cause != nil {
		return domain.BinaryInfo{}, errors.Join(cause, err)
	}
	return info, err
}

// The pinned native core.VersionStatement prints these fields on its first line.
// Unknown formats fail explicitly; no version/capability inference is attempted.
var versionLine = regexp.MustCompile(`^Xray (\S+) \(.+\) (\S+) \((go\S+) ([^/\s]+)/([^/\s)]+)\)$`)

func parseVersionOutput(output []byte) (domain.BinaryInfo, error) {
	line, _, _ := strings.Cut(string(output), "\n")
	fields := versionLine.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
	if fields == nil {
		return domain.BinaryInfo{}, domain.ErrInvalidInfoOutput
	}
	return domain.BinaryInfo{
		Version: fields[1], Build: fields[2], GoVersion: fields[3],
		OperatingSystem: fields[4], Architecture: fields[5], ObservedAt: time.Now().UTC(),
	}, nil
}

// The stdout copier is the only writer. Cmd.Run joins it before fields are read.
type versionOutput struct {
	limit    int
	data     []byte
	exceeded bool
}

func (w *versionOutput) Write(value []byte) (int, error) {
	count := min(len(value), w.limit-len(w.data))
	w.data = append(w.data, value[:count]...)
	if count != len(value) {
		w.exceeded = true
		return count, domain.ErrInfoOutputLimit
	}
	return count, nil
}
