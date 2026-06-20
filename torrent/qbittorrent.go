package torrent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"go.uber.org/fx"
)

// qbittorrentBackend adapts autobrr/go-qbittorrent to [Client]. The WebUI uses
// a SID session cookie; login happens once via fx OnStart, and the library
// transparently re-logs-in if the cookie expires.
type qbittorrentBackend struct {
	cli    *qbt.Client
	logger *slog.Logger
}

func newQBittorrentClient(lc fx.Lifecycle, opts Options, logger *slog.Logger) (*qbittorrentBackend, error) {
	if opts.QBittorrent.Host == "" {
		return nil, errors.New("torrent: qbittorrent host is required")
	}
	cfg := qbt.Config{
		Host:          opts.QBittorrent.Host,
		Username:      opts.QBittorrent.Username,
		Password:      opts.QBittorrent.Password,
		TLSSkipVerify: opts.QBittorrent.InsecureSkipVerify,
		// Timeout is seconds, not time.Duration, in this library.
		Timeout: int(opts.Timeout.Seconds()),
	}
	cli := qbt.NewClient(cfg).WithHTTPClient(
		newHTTPClient(opts.Timeout, opts.QBittorrent.InsecureSkipVerify),
	)
	b := &qbittorrentBackend{cli: cli, logger: logger}
	// Bounded session login on start; fx applies its own start timeout.
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := cli.LoginCtx(ctx); err != nil {
				return fmt.Errorf("torrent: qbittorrent login: %w", err)
			}
			logger.DebugContext(
				ctx, "torrent: qbittorrent logged in",
				slog.String("host", opts.QBittorrent.Host),
			)
			return nil
		},
	})
	return b, nil
}

func (b *qbittorrentBackend) Add(ctx context.Context, magnetOrURL string, opts AddOptions) (string, error) {
	if _, err := b.cli.AddTorrentsFromUrlsCtx(ctx, []string{magnetOrURL}, qbittorrentAddOptions(opts)); err != nil {
		return "", fmt.Errorf("torrent: qbittorrent add: %w", err)
	}
	// qBittorrent's add endpoint does not return a hash synchronously.
	return "", nil
}

func (b *qbittorrentBackend) AddFile(ctx context.Context, data []byte, opts AddOptions) (string, error) {
	if _, err := b.cli.AddTorrentFromMemoryCtx(ctx, data, qbittorrentAddOptions(opts)); err != nil {
		return "", fmt.Errorf("torrent: qbittorrent add file: %w", err)
	}
	return "", nil
}

// qbittorrentAddOptions maps AddOptions onto qBittorrent's string option map.
func qbittorrentAddOptions(opts AddOptions) map[string]string {
	m := map[string]string{}
	if opts.Paused {
		m["paused"] = "true"
		m["stopped"] = "true" // WebAPI 2.11 renamed paused -> stopped; send both.
	}
	if opts.SavePath != "" {
		m["savepath"] = opts.SavePath
	}
	if opts.Label != "" {
		m["category"] = opts.Label
	}
	return m
}

func (b *qbittorrentBackend) Remove(ctx context.Context, hash string, deleteData bool) error {
	if err := b.cli.DeleteTorrentsCtx(ctx, []string{strings.ToLower(hash)}, deleteData); err != nil {
		return fmt.Errorf("torrent: qbittorrent remove: %w", err)
	}
	return nil
}

func (b *qbittorrentBackend) List(ctx context.Context) ([]Torrent, error) {
	raw, err := b.cli.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{})
	if err != nil {
		return nil, fmt.Errorf("torrent: qbittorrent list: %w", err)
	}
	out := make([]Torrent, 0, len(raw))
	for i := range raw {
		out = append(out, mapQBittorrentTorrent(raw[i]))
	}
	return out, nil
}

func (b *qbittorrentBackend) Get(ctx context.Context, hash string) (Torrent, error) {
	raw, err := b.cli.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{Hashes: []string{strings.ToLower(hash)}})
	if err != nil {
		return Torrent{}, fmt.Errorf("torrent: qbittorrent get: %w", err)
	}
	if len(raw) == 0 {
		return Torrent{}, ErrNotFound
	}
	return mapQBittorrentTorrent(raw[0]), nil
}

func (b *qbittorrentBackend) Pause(ctx context.Context, hash string) error {
	if err := b.cli.StopCtx(ctx, []string{strings.ToLower(hash)}); err != nil {
		return fmt.Errorf("torrent: qbittorrent pause: %w", err)
	}
	return nil
}

func (b *qbittorrentBackend) Resume(ctx context.Context, hash string) error {
	if err := b.cli.StartCtx(ctx, []string{strings.ToLower(hash)}); err != nil {
		return fmt.Errorf("torrent: qbittorrent resume: %w", err)
	}
	return nil
}

func (b *qbittorrentBackend) Stats(ctx context.Context) (Stats, error) {
	info, err := b.cli.GetTransferInfoCtx(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("torrent: qbittorrent stats: %w", err)
	}
	list, err := b.cli.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{})
	if err != nil {
		return Stats{}, fmt.Errorf("torrent: qbittorrent stats count: %w", err)
	}
	return Stats{
		TorrentCount: len(list),
		DownloadRate: info.DlInfoSpeed,
		UploadRate:   info.UpInfoSpeed,
	}, nil
}

func mapQBittorrentTorrent(t qbt.Torrent) Torrent {
	return Torrent{
		Hash:         t.Hash,
		Name:         t.Name,
		State:        mapQBittorrentState(t.State),
		SizeBytes:    t.Size,
		Progress:     t.Progress,
		Ratio:        t.Ratio,
		DownloadRate: t.DlSpeed,
		UploadRate:   t.UpSpeed,
		AddedAt:      time.Unix(t.AddedOn, 0).UTC(),
	}
}

func mapQBittorrentState(s qbt.TorrentState) State {
	switch s {
	case qbt.TorrentStateError, qbt.TorrentStateMissingFiles:
		return StateError
	case qbt.TorrentStatePausedUp, qbt.TorrentStateStoppedUp,
		qbt.TorrentStatePausedDl, qbt.TorrentStateStoppedDl:
		return StatePaused
	case qbt.TorrentStateQueuedUp, qbt.TorrentStateQueuedDl:
		return StateQueued
	case qbt.TorrentStateCheckingUp, qbt.TorrentStateCheckingDl,
		qbt.TorrentStateCheckingResumeData:
		return StateChecking
	case qbt.TorrentStateUploading, qbt.TorrentStateStalledUp, qbt.TorrentStateForcedUp:
		return StateSeeding
	case qbt.TorrentStateDownloading, qbt.TorrentStateMetaDl,
		qbt.TorrentStateStalledDl, qbt.TorrentStateForcedDl, qbt.TorrentStateAllocating:
		return StateDownloading
	case qbt.TorrentStateMoving, qbt.TorrentStateUnknown:
		return StateUnknown
	default:
		return StateUnknown
	}
}
