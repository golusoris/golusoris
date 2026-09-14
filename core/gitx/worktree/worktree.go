// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package worktree manages ephemeral git worktrees under a repository — one
// per task, on its own branch — so agents and CI jobs work in isolation and
// are cleaned up deterministically. It is a thin, bounded wrapper over
// `git worktree` built on gitx. Capability key: git.worktree.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/golusoris/golusoris/core/gitx"
)

// Defaults and bounds.
const (
	// DefaultDir is the repository-relative directory holding worktrees.
	DefaultDir = ".golusoris/worktrees"
	// DefaultBranchPrefix prefixes every per-task branch.
	DefaultBranchPrefix = "wt/"
	// MaxTaskIDLen bounds task identifiers.
	MaxTaskIDLen = 128
	// MaxPorcelainLines bounds `git worktree list --porcelain` parsing.
	MaxPorcelainLines = 50_000
)

// Sentinel errors. Compare with errors.Is.
var (
	ErrEmptyTaskID   = errors.New("worktree: task id must not be empty")
	ErrTaskIDTooLong = errors.New("worktree: task id too long")
	ErrInvalidTaskID = errors.New("worktree: task id must match [A-Za-z0-9_-]+")
	ErrInvalidBase   = errors.New("worktree: invalid base ref")
	ErrTooManyLines  = errors.New("worktree: porcelain output exceeds line limit")
)

var taskIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Worktree is a created per-task workspace.
type Worktree struct {
	TaskID string `json:"task_id"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Base   string `json:"base,omitempty"`
}

// Info is one entry of `git worktree list --porcelain`.
type Info struct {
	Path        string `json:"path"`
	HEAD        string `json:"head"`
	Branch      string `json:"branch,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Bare        bool   `json:"bare,omitempty"`
	Detached    bool   `json:"detached,omitempty"`
	Locked      bool   `json:"locked,omitempty"`
	LockReason  string `json:"lock_reason,omitempty"`
	Prunable    bool   `json:"prunable,omitempty"`
	PruneReason string `json:"prune_reason,omitempty"`
}

// Manager creates and removes worktrees under root/<dir>/<task-id>.
type Manager struct {
	git    *gitx.Runner
	root   string
	dir    string
	prefix string
	mu     sync.Mutex
}

// Option configures a Manager.
type Option func(*Manager)

// WithDir sets the repository-relative worktree directory (default DefaultDir).
func WithDir(rel string) Option {
	return func(m *Manager) {
		if rel != "" {
			m.dir = filepath.FromSlash(rel)
		}
	}
}

// WithBranchPrefix sets the per-task branch prefix (default DefaultBranchPrefix).
func WithBranchPrefix(p string) Option {
	return func(m *Manager) { m.prefix = p }
}

// WithRunner supplies a pre-configured gitx.Runner (timeouts, binary).
func WithRunner(r *gitx.Runner) Option {
	return func(m *Manager) {
		if r != nil {
			m.git = r
		}
	}
}

// New returns a Manager rooted at the repository directory root.
func New(root string, opts ...Option) *Manager {
	clean := filepath.Clean(root)
	m := &Manager{root: clean, dir: filepath.FromSlash(DefaultDir), prefix: DefaultBranchPrefix}
	for _, o := range opts {
		o(m)
	}
	if m.git == nil {
		m.git = gitx.New(clean)
	}
	return m
}

// Root returns the repository directory.
func (m *Manager) Root() string { return m.root }

// Path returns the filesystem path a task's worktree lives at.
func (m *Manager) Path(taskID string) string {
	return filepath.Join(m.root, m.dir, taskID)
}

// Create runs `git worktree add -b <prefix><taskID> <path> <base>`.
func (m *Manager) Create(ctx context.Context, taskID, base string) (*Worktree, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return nil, err
	}
	if !gitx.ValidRef(base) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidBase, base)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	path := m.Path(taskID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("worktree: mkdir %s: %w", filepath.Dir(path), err)
	}
	branch := m.prefix + taskID
	if _, err := m.git.Run(ctx, "worktree", "add", "-b", branch, path, base); err != nil {
		return nil, fmt.Errorf("worktree: create %s: %w", taskID, err)
	}
	return &Worktree{TaskID: taskID, Branch: branch, Path: path, Base: base}, nil
}

// Remove runs `git worktree remove [--force] <path>` then deletes the branch.
func (m *Manager) Remove(ctx context.Context, taskID string, force bool) error {
	if err := ValidateTaskID(taskID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, m.Path(taskID))
	if _, err := m.git.Run(ctx, args...); err != nil {
		return fmt.Errorf("worktree: remove %s: %w", taskID, err)
	}
	if _, err := m.git.Run(ctx, "branch", "-D", m.prefix+taskID); err != nil {
		return fmt.Errorf("worktree: delete branch %s: %w", m.prefix+taskID, err)
	}
	return nil
}

// List parses `git worktree list --porcelain`.
func (m *Manager) List(ctx context.Context) ([]Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, err := m.git.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("worktree: list: %w", err)
	}
	return ParseList(string(out))
}

// Prune runs `git worktree prune`.
func (m *Manager) Prune(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.git.Run(ctx, "worktree", "prune"); err != nil {
		return fmt.Errorf("worktree: prune: %w", err)
	}
	return nil
}

// ValidateTaskID enforces the task identifier contract.
func ValidateTaskID(id string) error {
	switch {
	case id == "":
		return ErrEmptyTaskID
	case len(id) > MaxTaskIDLen:
		return fmt.Errorf("%w: %d > %d", ErrTaskIDTooLong, len(id), MaxTaskIDLen)
	case !taskIDRE.MatchString(id):
		return fmt.Errorf("%w: %q", ErrInvalidTaskID, id)
	}
	return nil
}

// ParseList parses porcelain worktree output (blank-line separated records).
func ParseList(out string) ([]Info, error) {
	lines := strings.Split(out, "\n")
	if len(lines) > MaxPorcelainLines {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooManyLines, len(lines), MaxPorcelainLines)
	}
	var (
		result  []Info
		current Info
		open    bool
	)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			if open {
				result = append(result, current)
				current, open = Info{}, false
			}
			continue
		}
		open = true
		applyAttr(&current, line)
	}
	if open {
		result = append(result, current)
	}
	return result, nil
}

func applyAttr(info *Info, line string) {
	key, val, _ := strings.Cut(line, " ")
	switch key {
	case "worktree":
		info.Path = val
	case "HEAD":
		info.HEAD = val
	case "branch":
		info.Ref = val
		info.Branch = strings.TrimPrefix(val, "refs/heads/")
	case "bare":
		info.Bare = true
	case "detached":
		info.Detached = true
	case "locked":
		info.Locked, info.LockReason = true, val
	case "prunable":
		info.Prunable, info.PruneReason = true, val
	}
}
