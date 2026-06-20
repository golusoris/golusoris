package torrent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	rt "github.com/autobrr/go-rtorrent"
)

// rtViewMain is rtorrent's default "all torrents" view.
const rtViewMain rt.View = "main"

// rtorrentBackend adapts autobrr/go-rtorrent (XML-RPC) to [Client]. rtorrent's
// API addresses torrents by an rt.Torrent value, so mutating operations first
// resolve the torrent by hash.
type rtorrentBackend struct {
	cli    *rt.Client
	logger *slog.Logger
}

func newRTorrentClient(opts Options, logger *slog.Logger) (*rtorrentBackend, error) {
	if opts.RTorrent.Addr == "" {
		return nil, errors.New("torrent: rtorrent addr is required")
	}
	cli := rt.NewClient(rt.Config{
		Addr:          opts.RTorrent.Addr,
		BasicUser:     opts.RTorrent.BasicUser,
		BasicPass:     opts.RTorrent.BasicPass,
		TLSSkipVerify: opts.RTorrent.InsecureSkipVerify,
	}).WithHTTPClient(newHTTPClient(opts.Timeout, opts.RTorrent.InsecureSkipVerify))
	return &rtorrentBackend{cli: cli, logger: logger}, nil
}

func (b *rtorrentBackend) Add(ctx context.Context, magnetOrURL string, opts AddOptions) (string, error) {
	args := rtorrentAddArgs(opts)
	add := b.cli.Add
	if opts.Paused {
		add = b.cli.AddStopped
	}
	if err := add(ctx, magnetOrURL, args...); err != nil {
		return "", fmt.Errorf("torrent: rtorrent add: %w", err)
	}
	// rtorrent does not return a hash synchronously when adding by magnet/URL.
	return "", nil
}

func (b *rtorrentBackend) AddFile(ctx context.Context, data []byte, opts AddOptions) (string, error) {
	args := rtorrentAddArgs(opts)
	add := b.cli.AddTorrent
	if opts.Paused {
		add = b.cli.AddTorrentStopped
	}
	if err := add(ctx, data, args...); err != nil {
		return "", fmt.Errorf("torrent: rtorrent add file: %w", err)
	}
	return "", nil
}

// rtorrentAddArgs maps AddOptions onto rtorrent extra XML-RPC field/value args.
func rtorrentAddArgs(opts AddOptions) []*rt.FieldValue {
	var args []*rt.FieldValue
	if opts.SavePath != "" {
		args = append(args, &rt.FieldValue{Field: rt.DDirectory, Value: opts.SavePath})
	}
	if opts.Label != "" {
		args = append(args, &rt.FieldValue{Field: rt.DLabel, Value: opts.Label})
	}
	return args
}

func (b *rtorrentBackend) Remove(ctx context.Context, hash string, _ bool) error {
	t, err := b.lookup(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // removing a missing torrent is not an error
		}
		return err
	}
	if delErr := b.cli.Delete(ctx, t); delErr != nil {
		return fmt.Errorf("torrent: rtorrent remove: %w", delErr)
	}
	return nil
}

func (b *rtorrentBackend) List(ctx context.Context) ([]Torrent, error) {
	raw, err := b.cli.GetTorrents(ctx, rtViewMain)
	if err != nil {
		return nil, fmt.Errorf("torrent: rtorrent list: %w", err)
	}
	out := make([]Torrent, 0, len(raw))
	for i := range raw {
		out = append(out, b.mapTorrent(ctx, raw[i]))
	}
	return out, nil
}

func (b *rtorrentBackend) Get(ctx context.Context, hash string) (Torrent, error) {
	t, err := b.lookup(ctx, hash)
	if err != nil {
		return Torrent{}, err
	}
	return b.mapTorrent(ctx, t), nil
}

func (b *rtorrentBackend) Pause(ctx context.Context, hash string) error {
	t, err := b.lookup(ctx, hash)
	if err != nil {
		return err
	}
	if pErr := b.cli.PauseTorrent(ctx, t); pErr != nil {
		return fmt.Errorf("torrent: rtorrent pause: %w", pErr)
	}
	return nil
}

func (b *rtorrentBackend) Resume(ctx context.Context, hash string) error {
	t, err := b.lookup(ctx, hash)
	if err != nil {
		return err
	}
	if rErr := b.cli.ResumeTorrent(ctx, t); rErr != nil {
		return fmt.Errorf("torrent: rtorrent resume: %w", rErr)
	}
	return nil
}

func (b *rtorrentBackend) Stats(ctx context.Context) (Stats, error) {
	raw, err := b.cli.GetTorrents(ctx, rtViewMain)
	if err != nil {
		return Stats{}, fmt.Errorf("torrent: rtorrent stats: %w", err)
	}
	down, err := b.cli.DownRate(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("torrent: rtorrent down rate: %w", err)
	}
	up, err := b.cli.UpRate(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("torrent: rtorrent up rate: %w", err)
	}
	return Stats{
		TorrentCount: len(raw),
		DownloadRate: int64(down),
		UploadRate:   int64(up),
	}, nil
}

// lookup resolves a torrent by info-hash. rtorrent indexes by uppercase hash.
// GetTorrent echoes the queried hash back regardless of existence, so an empty
// name is the signal that rtorrent does not know the torrent.
func (b *rtorrentBackend) lookup(ctx context.Context, hash string) (rt.Torrent, error) {
	t, err := b.cli.GetTorrent(ctx, strings.ToUpper(hash))
	if err != nil {
		return rt.Torrent{}, fmt.Errorf("torrent: rtorrent lookup: %w", err)
	}
	if t.Name == "" {
		return rt.Torrent{}, ErrNotFound
	}
	return t, nil
}

// mapTorrent maps an rt.Torrent onto the normalised view. Per-torrent status
// is fetched lazily; a status error degrades gracefully to the list-level data.
func (b *rtorrentBackend) mapTorrent(ctx context.Context, t rt.Torrent) Torrent {
	out := Torrent{
		Hash:      strings.ToLower(t.Hash),
		Name:      t.Name,
		SizeBytes: int64(t.Size),
		Ratio:     t.Ratio,
		State:     rtorrentState(t, false),
		AddedAt:   t.Started,
	}
	st, err := b.cli.GetStatus(ctx, t)
	if err != nil {
		b.logger.DebugContext(
			ctx, "torrent: rtorrent status fetch failed",
			slog.String("hash", out.Hash),
			slog.String("error", err.Error()),
		)
		return out
	}
	out.DownloadRate = int64(st.DownRate)
	out.UploadRate = int64(st.UpRate)
	if st.Size > 0 {
		out.Progress = float64(st.CompletedBytes) / float64(st.Size)
	}
	out.State = rtorrentState(t, st.Completed)
	return out
}

// rtorrentState derives a normalised state from list-level flags. rtorrent has
// no single "state" field; completion + the active flag approximate it.
func rtorrentState(t rt.Torrent, completed bool) State {
	switch {
	case completed || t.Completed:
		return StateSeeding
	default:
		return StateDownloading
	}
}
