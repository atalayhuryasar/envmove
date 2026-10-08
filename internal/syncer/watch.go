package syncer

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WatchOptions configures the idle publisher.
type WatchOptions struct {
	// Quiet is how long every carried file has to be untouched before a sync runs. The
	// default matters more than it looks: an agent rewrites a .env five times in a row,
	// and publishing on every write would ship a half-finished file.
	Quiet time.Duration

	// Every is how often the files are checked. It is a stat, not a read.
	Every time.Duration

	// OnChange is called with a short line whenever a sync actually moved something, so
	// an idle watch does not narrate itself.
	OnChange func(string)

	// OnError is called when a sync fails. Without it the loop would retry a failing
	// sync silently forever, which looks exactly like the tool is working.
	OnError func(error)
}

type fileStamp struct {
	modTime time.Time
	size    int64
}

// Watch publishes the carried files once they have stopped changing.
//
// The obvious design, pushing on every write, was rejected earlier for a good reason:
// an agent rewrites the same file repeatedly, so each change would publish a partial
// file and a conflict could appear while nobody was looking. Debouncing inverts that.
// A file is published only after it has been completely quiet for Quiet, which means
// half-finished states exist on disk but never on the remote.
func (s *Syncer) Watch(opts WatchOptions, stop <-chan struct{}) error {
	if opts.Quiet <= 0 {
		opts.Quiet = 60 * time.Second
	}
	if opts.Every <= 0 {
		opts.Every = 5 * time.Second
	}
	if opts.OnChange == nil {
		opts.OnChange = func(string) {}
	}
	if opts.OnError == nil {
		opts.OnError = func(error) {}
	}
	if opts.OnError == nil {
		opts.OnError = func(error) {}
	}

	last := s.stamps()
	var quietSince time.Time

	ticker := time.NewTicker(opts.Every)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
		}

		now := s.stamps()
		if !sameStamps(last, now) {
			last = now
			quietSince = time.Time{}
			continue
		}
		if quietSince.IsZero() {
			quietSince = time.Now()
			continue
		}
		if time.Since(quietSince) < opts.Quiet {
			continue
		}
		quietSince = time.Time{}

		d, published, err := s.Sync()
		if err != nil {
			opts.OnError(err)
			continue
		}
		for _, c := range d.Changes {
			switch c.Kind {
			case Incoming:
				opts.OnChange("downloaded " + c.Path)
			case Outgoing:
				opts.OnChange("uploaded " + c.Path)
			case Conflict:
				opts.OnChange("conflict on " + c.Path + ", left alone")
			}
		}
		if published {
			opts.OnChange(fmt.Sprintf("pushed to %s", s.Ref()))
		}
	}
}

// stamps is the current shape of the carried files, by modification time and size.
// Content is deliberately not read: this runs every few seconds, indefinitely.
func (s *Syncer) stamps() map[string]fileStamp {
	out := make(map[string]fileStamp, len(s.cfg.Paths))
	for _, rel := range s.cfg.Paths {
		info, err := os.Stat(filepath.Join(s.repo.Root, rel))
		if err != nil {
			continue
		}
		out[rel] = fileStamp{modTime: info.ModTime(), size: info.Size()}
	}
	return out
}

func sameStamps(a, b map[string]fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		other, ok := b[k]
		if !ok || other.modTime != v.modTime || other.size != v.size {
			return false
		}
	}
	return true
}

// Sync is pull then push, the full round trip. Session start and watch use it because
// they need to catch up on the other machine as well as publish.
func (s *Syncer) Sync() (*Diff, bool, error) {
	if _, _, err := s.Pull(); err != nil {
		return nil, false, err
	}
	return s.Push()
}
