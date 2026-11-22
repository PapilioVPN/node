//go:build linux

// Package store owns private immutable release files and synced activation intent.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"golang.org/x/sys/unix"
)

type Store struct {
	root *os.Root
	lock *os.File
	id   string
	// gate rejects overlapping I/O; it is not an observation-state lock.
	gate sync.Mutex
}

// Open requires an existing dedicated private directory. It never imports,
// rewrites or deletes operator configs, source leases, certificates or live data.
// One process owns the directory lock until Close after service operations join.
func Open(path, id string) (*Store, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || !filepath.IsAbs(path) || !info.IsDir() || info.Mode().Perm() != 0700 || !validators.ID(id) {
		return nil, domain.ErrInvalidArgument
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	f, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		root.Close()
		return nil, errors.Join(domain.ErrBusy, err)
	}
	s := &Store{root: root, lock: f, id: id}
	if err = root.Mkdir("releases", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		s.Close()
		return nil, err
	}
	info, err = root.Lstat("releases")
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		s.Close()
		return nil, domain.ErrInvalidArgument
	}
	return s, nil
}
func (s *Store) Close() error          { return errors.Join(s.root.Close(), s.lock.Close()) }
func (s *Store) Path(id string) string { return filepath.Join(s.root.Name(), "releases", id) }
func (s *Store) lockIO(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.gate.TryLock() {
		return nil, domain.ErrBusy
	}
	return s.gate.Unlock, nil
}
func artifact(f domain.File, entries uint32) domain.Artifact {
	return domain.Artifact{SHA256: f.SHA256, Bytes: uint64(len(f.Content)), Entries: entries}
}
func candidateID(c domain.Candidate) string {
	sum := sha256.Sum256([]byte(c.Binary.SHA256 + c.GeoIP.SHA256 + c.GeoSite.SHA256))
	return hex.EncodeToString(sum[:])
}

func (s *Store) Stage(ctx context.Context, c domain.Candidate) (domain.Release, error) {
	if err := ctx.Err(); err != nil {
		return domain.Release{}, err
	}
	if err := validators.Candidate(c); err != nil {
		return domain.Release{}, err
	}
	release, err := s.lockIO(ctx)
	if err != nil {
		return domain.Release{}, err
	}
	defer release()
	ipCount, siteCount, err := geodataInfo(ctx, c.GeoIP.Content, c.GeoSite.Content)
	if err != nil {
		return domain.Release{}, err
	}
	id := candidateID(c)
	if _, err := s.root.Lstat("releases/" + id); err == nil {
		return s.readRelease(ctx, id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return domain.Release{}, err
	}
	entries, err := s.root.Open("releases")
	if err != nil {
		return domain.Release{}, err
	}
	names, readErr := entries.Readdirnames(domain.MaxReleases + 1)
	entries.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return domain.Release{}, readErr
	}
	if len(names) >= domain.MaxReleases {
		return domain.Release{}, domain.ErrCapacity
	}
	dir, err := os.MkdirTemp(filepath.Join(s.root.Name(), "releases"), ".stage-")
	if err != nil {
		return domain.Release{}, err
	}
	defer os.RemoveAll(dir)
	for _, v := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{{"xray", c.Binary.Content, 0700}, {"geoip.dat", c.GeoIP.Content, 0600}, {"geosite.dat", c.GeoSite.Content, 0600}} {
		if err = writeFile(ctx, filepath.Join(dir, v.name), v.data, v.mode); err != nil {
			return domain.Release{}, err
		}
	}
	ver, err := version(ctx, filepath.Join(dir, "xray"))
	if err != nil {
		return domain.Release{}, err
	}
	if err = nativeCheck(ctx, dir, c.GeoIP.Content, c.GeoSite.Content); err != nil {
		return domain.Release{}, err
	}
	result := domain.Release{ID: id, Version: ver, Binary: artifact(c.Binary, 0), GeoIP: artifact(c.GeoIP, ipCount), GeoSite: artifact(c.GeoSite, siteCount), PreparedAt: time.Now().UTC()}
	data, err := json.Marshal(result)
	if err != nil {
		return domain.Release{}, err
	}
	if err = writeFile(ctx, filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
		return domain.Release{}, err
	}
	if err = syncDirectory(dir); err != nil {
		return domain.Release{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.Release{}, err
	}
	if err = s.root.Rename("releases/"+filepath.Base(dir), "releases/"+id); err != nil {
		return domain.Release{}, err
	}
	if err = syncDirectory(filepath.Join(s.root.Name(), "releases")); err != nil {
		return domain.Release{}, err
	}
	return result, nil
}
func writeFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := f.Write(data[:min(len(data), 64<<10)])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *Store) read(name string, maxBytes int) ([]byte, error) {
	f, err := s.root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(maxBytes) || info.Mode().Perm()&0077 != 0 {
		return nil, domain.ErrRejected
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if len(data) > maxBytes {
		return nil, domain.ErrCapacity
	}
	return data, err
}
func (s *Store) readRelease(ctx context.Context, id string) (domain.Release, error) {
	if !validators.Digest(id) {
		return domain.Release{}, domain.ErrInvalidArgument
	}
	info, err := s.root.Lstat("releases/" + id)
	if errors.Is(err, os.ErrNotExist) {
		return domain.Release{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Release{}, err
	}
	if !info.IsDir() {
		return domain.Release{}, domain.ErrRejected
	}
	data, err := s.read("releases/"+id+"/manifest.json", 4096)
	if err != nil {
		return domain.Release{}, err
	}
	var r domain.Release
	if json.Unmarshal(data, &r) != nil || r.ID != id {
		return domain.Release{}, domain.ErrRejected
	}
	var c domain.Candidate
	for _, v := range []struct {
		name   string
		max    int
		meta   domain.Artifact
		target *domain.File
	}{{"xray", domain.MaxBinary, r.Binary, &c.Binary}, {"geoip.dat", domain.MaxGeodata, r.GeoIP, &c.GeoIP}, {"geosite.dat", domain.MaxGeodata, r.GeoSite, &c.GeoSite}} {
		if err := ctx.Err(); err != nil {
			return domain.Release{}, err
		}
		content, err := s.read("releases/"+id+"/"+v.name, v.max)
		if err != nil {
			return domain.Release{}, err
		}
		if uint64(len(content)) != v.meta.Bytes {
			return domain.Release{}, domain.ErrRejected
		}
		*v.target = domain.File{Content: content, SHA256: v.meta.SHA256}
	}
	if err := validators.Candidate(c); err != nil {
		return domain.Release{}, err
	}
	if candidateID(c) != id {
		return domain.Release{}, domain.ErrRejected
	}
	ip, site, err := geodataInfo(ctx, c.GeoIP.Content, c.GeoSite.Content)
	if err != nil {
		return domain.Release{}, err
	}
	if ip != r.GeoIP.Entries || site != r.GeoSite.Entries {
		return domain.Release{}, domain.ErrRejected
	}
	ver, err := version(ctx, filepath.Join(s.Path(id), "xray"))
	if err != nil {
		return domain.Release{}, err
	}
	if ver != r.Version {
		return domain.Release{}, domain.ErrRejected
	}
	return r, nil
}
func (s *Store) Release(ctx context.Context, id string) (domain.Release, error) {
	release, err := s.lockIO(ctx)
	if err != nil {
		return domain.Release{}, err
	}
	defer release()
	return s.readRelease(ctx, id)
}
func (s *Store) state() (domain.State, error) {
	data, err := s.read("state.json", 4096)
	if errors.Is(err, os.ErrNotExist) {
		return domain.State{InstanceID: s.id}, nil
	}
	if err != nil {
		return domain.State{}, err
	}
	var state domain.State
	if json.Unmarshal(data, &state) != nil || state.InstanceID != s.id || !validators.Digest(state.Active) || (state.Previous != "" && !validators.Digest(state.Previous)) || (state.Pending != "" && !validators.Digest(state.Pending)) || state.Revision == 0 {
		return domain.State{}, domain.ErrRejected
	}
	return state, nil
}
func (s *Store) Inspect(ctx context.Context) (domain.Inventory, error) {
	release, err := s.lockIO(ctx)
	if err != nil {
		return domain.Inventory{}, err
	}
	defer release()
	state, err := s.state()
	if err != nil {
		return domain.Inventory{}, err
	}
	result := domain.Inventory{State: state, ObservedAt: time.Now().UTC()}
	if state.Active != "" {
		link, err := s.root.Readlink("current")
		if err != nil {
			return result, err
		}
		selected := ""
		if link == "releases/"+state.Active {
			selected = state.Active
		} else if state.Pending != "" && link == "releases/"+state.Pending {
			selected = state.Pending
		} else {
			return result, domain.ErrRecovery
		}
		result.Selected = selected
		r, err := s.readRelease(ctx, selected)
		if err != nil {
			return domain.Inventory{}, err
		}
		result.Installed = &r
	}
	return result, nil
}

// Initialize is an offline installer operation for an empty dedicated root. The
// caller must have no running instance; runtime controllers use Activate instead.
func (s *Store) Initialize(ctx context.Context, id string) error {
	release, err := s.lockIO(ctx)
	if err != nil {
		return err
	}
	defer release()
	state, err := s.state()
	if err != nil {
		return err
	}
	if state.Revision != 0 {
		return domain.ErrConflict
	}
	if _, err = s.readRelease(ctx, id); err != nil {
		return err
	}
	if err = s.selectRelease(id); err != nil {
		return err
	}
	return s.save(domain.State{InstanceID: s.id, Revision: 1, Active: id})
}
func (s *Store) Begin(ctx context.Context, expected uint64, target string) (domain.State, error) {
	release, err := s.lockIO(ctx)
	if err != nil {
		return domain.State{}, err
	}
	defer release()
	state, err := s.state()
	if err != nil {
		return state, err
	}
	if state.Revision == 0 || state.Revision != expected || state.Revision == math.MaxUint64 || state.Active == target {
		return state, domain.ErrConflict
	}
	if state.Pending != "" {
		return state, domain.ErrRecovery
	}
	if _, err = s.readRelease(ctx, target); err != nil {
		return state, err
	}
	// Inspect current pointer before any stop or replacement.
	link, err := s.root.Readlink("current")
	if err != nil || link != "releases/"+state.Active {
		return state, domain.ErrRecovery
	}
	state.Revision++
	state.Pending = target
	return state, s.save(state)
}
func (s *Store) selectRelease(id string) error {
	if !validators.Digest(id) {
		return domain.ErrInvalidArgument
	}
	// Never replace an operator-owned ordinary file/directory.
	if info, err := s.root.Lstat("current"); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return domain.ErrConflict
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.MkdirTemp(s.root.Name(), ".select-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	name := filepath.Base(temp) + "/link"
	if err = s.root.Symlink("releases/"+id, name); err != nil {
		return err
	}
	if err = s.root.Rename(name, "current"); err != nil {
		return err
	}
	return s.lock.Sync()
}
func (s *Store) Select(ctx context.Context, id string) error {
	release, err := s.lockIO(ctx)
	if err != nil {
		return err
	}
	defer release()
	state, err := s.state()
	if err != nil {
		return err
	}
	if state.Pending == "" || (id != state.Pending && id != state.Active) {
		return domain.ErrConflict
	}
	return s.selectRelease(id)
}
func (s *Store) Finish(ctx context.Context, expected uint64, success bool) (domain.State, error) {
	release, err := s.lockIO(ctx)
	if err != nil {
		return domain.State{}, err
	}
	defer release()
	state, err := s.state()
	if err != nil {
		return state, err
	}
	if state.Revision != expected || state.Pending == "" {
		return state, domain.ErrConflict
	}
	selected := state.Active
	if success {
		selected = state.Pending
	}
	link, err := s.root.Readlink("current")
	if err != nil || link != "releases/"+selected {
		return state, domain.ErrRecovery
	}
	if success {
		state.Previous = state.Active
		state.Active = state.Pending
	}
	state.Pending = ""
	return state, s.save(state)
}
func (s *Store) save(state domain.State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp(s.root.Name(), ".state-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err = writeFile(context.Background(), filepath.Join(temp, "record"), data, 0600); err != nil {
		return err
	}
	if err = s.root.Rename(filepath.Base(temp)+"/record", "state.json"); err != nil {
		return err
	}
	return s.lock.Sync() // A sync error is uncertain publication: callers must read back.
}

// State reads durable recovery authority without executing a possibly damaged candidate.
func (s *Store) State(ctx context.Context) (domain.State, error) {
	release, err := s.lockIO(ctx)
	if err != nil {
		return domain.State{}, err
	}
	defer release()
	return s.state()
}
