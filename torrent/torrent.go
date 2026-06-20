// Package torrent provides a backend-agnostic Client abstraction over a
// running torrent daemon. Apps add, remove, list, inspect, pause and resume
// torrents through a single [Client] interface; the concrete backend
// (rtorrent, qbittorrent, transmission) is selected by config, mirroring how
// storage/ selects local vs s3.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    torrent.Module, // provides torrent.Client
//	)
//
// Config keys live under the "torrent" prefix. See [Options].
package torrent

import (
	"context"
	"errors"
	"time"
)

// ErrUnsupportedBackend is returned by [newClient] when Options.Backend names a
// backend that is recognised but not compiled in, or is unknown.
var ErrUnsupportedBackend = errors.New("torrent: unsupported backend")

// ErrNotFound is returned when a torrent identified by hash does not exist on
// the daemon.
var ErrNotFound = errors.New("torrent: torrent not found")

// State is the normalised lifecycle state of a torrent, mapped from each
// backend's native vocabulary onto a small shared set.
type State string

const (
	// StateUnknown is the zero value: the backend reported a state this
	// abstraction does not map.
	StateUnknown State = "unknown"
	// StateDownloading means the torrent is actively fetching data.
	StateDownloading State = "downloading"
	// StateSeeding means the download is complete and the torrent is uploading.
	StateSeeding State = "seeding"
	// StatePaused means the torrent is stopped/paused by the user.
	StatePaused State = "paused"
	// StateChecking means the torrent is hash-checking its data.
	StateChecking State = "checking"
	// StateQueued means the torrent is waiting in a queue to start.
	StateQueued State = "queued"
	// StateError means the backend flagged the torrent as errored.
	StateError State = "error"
)

// AddOptions tunes how a torrent is added.
type AddOptions struct {
	// Paused adds the torrent in a stopped state instead of starting it.
	Paused bool
	// SavePath overrides the daemon's default download directory. Empty means
	// "use the daemon default". Not honoured by every backend.
	SavePath string
	// Label tags the torrent (category/label). Not honoured by every backend.
	Label string
}

// Torrent is the normalised view of a single torrent across backends.
type Torrent struct {
	// Hash is the lowercase info-hash; the stable cross-backend identifier.
	Hash string
	// Name is the torrent's display name.
	Name string
	// State is the normalised lifecycle state.
	State State
	// SizeBytes is the total size of the torrent's content in bytes.
	SizeBytes int64
	// Progress is the completion fraction in [0,1].
	Progress float64
	// Ratio is the share ratio (uploaded/downloaded).
	Ratio float64
	// DownloadRate is the current download speed in bytes/sec.
	DownloadRate int64
	// UploadRate is the current upload speed in bytes/sec.
	UploadRate int64
	// AddedAt is when the torrent was added, when the backend reports it.
	AddedAt time.Time
}

// Stats is a daemon-wide summary returned by [Client.Stats].
type Stats struct {
	// TorrentCount is the number of torrents known to the daemon.
	TorrentCount int
	// DownloadRate is the aggregate download speed in bytes/sec.
	DownloadRate int64
	// UploadRate is the aggregate upload speed in bytes/sec.
	UploadRate int64
}

// Client is the backend-agnostic torrent-daemon abstraction. Implementations
// must be safe for concurrent use by multiple goroutines.
type Client interface {
	// Add adds a torrent from a magnet link or an http(s) URL to a .torrent.
	// It returns the info-hash when the backend reports one (some backends do
	// not return a hash synchronously for magnets).
	Add(ctx context.Context, magnetOrURL string, opts AddOptions) (string, error)
	// AddFile adds a torrent from raw .torrent file bytes.
	AddFile(ctx context.Context, data []byte, opts AddOptions) (string, error)
	// Remove removes the torrent identified by hash. When deleteData is true,
	// the downloaded files are deleted too. Removing a missing torrent is not
	// an error.
	Remove(ctx context.Context, hash string, deleteData bool) error
	// List returns all torrents known to the daemon.
	List(ctx context.Context) ([]Torrent, error)
	// Get returns the torrent identified by hash, or [ErrNotFound].
	Get(ctx context.Context, hash string) (Torrent, error)
	// Pause stops the torrent identified by hash.
	Pause(ctx context.Context, hash string) error
	// Resume starts the torrent identified by hash.
	Resume(ctx context.Context, hash string) error
	// Stats returns a daemon-wide summary.
	Stats(ctx context.Context) (Stats, error)
}
