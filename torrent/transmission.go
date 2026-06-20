package torrent

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	transmissionrpc "github.com/hekmon/transmissionrpc/v3"
)

// transmissionBackend adapts hekmon/transmissionrpc/v3 to [Client]. The
// underlying library transparently handles the X-Transmission-Session-Id (409)
// CSRF handshake and is safe for concurrent use.
type transmissionBackend struct {
	rpc    *transmissionrpc.Client
	logger *slog.Logger
}

// torrentGetFields is the field set requested from TorrentGet so every mapped
// pointer is populated.
var torrentGetFields = []string{
	"id", "hashString", "name", "status", "totalSize", "sizeWhenDone",
	"percentDone", "uploadRatio", "rateDownload", "rateUpload", "addedDate",
}

func newTransmissionClient(opts Options, logger *slog.Logger) (*transmissionBackend, error) {
	if opts.Transmission.URL == "" {
		return nil, errors.New("torrent: transmission url is required")
	}
	endpoint, err := url.Parse(opts.Transmission.URL)
	if err != nil {
		return nil, fmt.Errorf("torrent: parse transmission url: %w", err)
	}
	rpc, err := transmissionrpc.New(endpoint, &transmissionrpc.Config{
		CustomClient: newHTTPClient(opts.Timeout, opts.Transmission.InsecureSkipVerify),
	})
	if err != nil {
		return nil, fmt.Errorf("torrent: init transmission rpc: %w", err)
	}
	return &transmissionBackend{rpc: rpc, logger: logger}, nil
}

// newHTTPClient builds an *http.Client with a mandatory request timeout. The
// timeout satisfies the http-client-must-set-timeout CI rule.
func newHTTPClient(timeout time.Duration, insecure bool) *http.Client {
	transport := http.DefaultTransport
	if insecure {
		// #nosec G402 -- opt-in InsecureSkipVerify is test-only config, off by default.
		transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func (b *transmissionBackend) Add(ctx context.Context, magnetOrURL string, opts AddOptions) (string, error) {
	payload := transmissionrpc.TorrentAddPayload{Filename: &magnetOrURL}
	applyTransmissionAddOptions(&payload, opts)
	t, err := b.rpc.TorrentAdd(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("torrent: transmission add: %w", err)
	}
	return derefString(t.HashString), nil
}

func (b *transmissionBackend) AddFile(ctx context.Context, data []byte, opts AddOptions) (string, error) {
	meta := base64.StdEncoding.EncodeToString(data)
	payload := transmissionrpc.TorrentAddPayload{MetaInfo: &meta}
	applyTransmissionAddOptions(&payload, opts)
	t, err := b.rpc.TorrentAdd(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("torrent: transmission add file: %w", err)
	}
	return derefString(t.HashString), nil
}

func applyTransmissionAddOptions(payload *transmissionrpc.TorrentAddPayload, opts AddOptions) {
	if opts.Paused {
		paused := true
		payload.Paused = &paused
	}
	if opts.SavePath != "" {
		dir := opts.SavePath
		payload.DownloadDir = &dir
	}
	if opts.Label != "" {
		payload.Labels = []string{opts.Label}
	}
}

func (b *transmissionBackend) Remove(ctx context.Context, hash string, deleteData bool) error {
	id, err := b.idForHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // removing a missing torrent is not an error
		}
		return err
	}
	payload := transmissionrpc.TorrentRemovePayload{IDs: []int64{id}, DeleteLocalData: deleteData}
	if rmErr := b.rpc.TorrentRemove(ctx, payload); rmErr != nil {
		return fmt.Errorf("torrent: transmission remove: %w", rmErr)
	}
	return nil
}

func (b *transmissionBackend) List(ctx context.Context) ([]Torrent, error) {
	raw, err := b.rpc.TorrentGet(ctx, torrentGetFields, nil)
	if err != nil {
		return nil, fmt.Errorf("torrent: transmission list: %w", err)
	}
	out := make([]Torrent, 0, len(raw))
	for i := range raw {
		out = append(out, mapTransmissionTorrent(raw[i]))
	}
	return out, nil
}

func (b *transmissionBackend) Get(ctx context.Context, hash string) (Torrent, error) {
	raw, err := b.rpc.TorrentGetAllForHashes(ctx, []string{strings.ToLower(hash)})
	if err != nil {
		return Torrent{}, fmt.Errorf("torrent: transmission get: %w", err)
	}
	if len(raw) == 0 {
		return Torrent{}, ErrNotFound
	}
	return mapTransmissionTorrent(raw[0]), nil
}

func (b *transmissionBackend) Pause(ctx context.Context, hash string) error {
	if err := b.rpc.TorrentStopHashes(ctx, []string{strings.ToLower(hash)}); err != nil {
		return fmt.Errorf("torrent: transmission pause: %w", err)
	}
	return nil
}

func (b *transmissionBackend) Resume(ctx context.Context, hash string) error {
	if err := b.rpc.TorrentStartHashes(ctx, []string{strings.ToLower(hash)}); err != nil {
		return fmt.Errorf("torrent: transmission resume: %w", err)
	}
	return nil
}

func (b *transmissionBackend) Stats(ctx context.Context) (Stats, error) {
	s, err := b.rpc.SessionStats(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("torrent: transmission stats: %w", err)
	}
	return Stats{
		TorrentCount: int(s.TorrentCount),
		DownloadRate: s.DownloadSpeed,
		UploadRate:   s.UploadSpeed,
	}, nil
}

// idForHash resolves a torrent's numeric id from its info-hash.
func (b *transmissionBackend) idForHash(ctx context.Context, hash string) (int64, error) {
	raw, err := b.rpc.TorrentGetHashes(ctx, []string{"id", "hashString"}, []string{strings.ToLower(hash)})
	if err != nil {
		return 0, fmt.Errorf("torrent: transmission resolve hash: %w", err)
	}
	if len(raw) == 0 || raw[0].ID == nil {
		return 0, ErrNotFound
	}
	return *raw[0].ID, nil
}

func mapTransmissionTorrent(t transmissionrpc.Torrent) Torrent {
	out := Torrent{
		Hash:         derefString(t.HashString),
		Name:         derefString(t.Name),
		State:        mapTransmissionState(t.Status),
		Ratio:        derefFloat(t.UploadRatio),
		Progress:     derefFloat(t.PercentDone),
		DownloadRate: derefInt(t.RateDownload),
		UploadRate:   derefInt(t.RateUpload),
	}
	if t.TotalSize != nil {
		out.SizeBytes = int64(t.TotalSize.Byte())
	}
	if t.AddedDate != nil {
		out.AddedAt = *t.AddedDate
	}
	return out
}

func mapTransmissionState(s *transmissionrpc.TorrentStatus) State {
	if s == nil {
		return StateUnknown
	}
	switch *s {
	case transmissionrpc.TorrentStatusStopped:
		return StatePaused
	case transmissionrpc.TorrentStatusCheckWait, transmissionrpc.TorrentStatusCheck:
		return StateChecking
	case transmissionrpc.TorrentStatusDownloadWait, transmissionrpc.TorrentStatusSeedWait:
		return StateQueued
	case transmissionrpc.TorrentStatusDownload:
		return StateDownloading
	case transmissionrpc.TorrentStatusSeed:
		return StateSeeding
	case transmissionrpc.TorrentStatusIsolated:
		return StateError
	default:
		return StateUnknown
	}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
