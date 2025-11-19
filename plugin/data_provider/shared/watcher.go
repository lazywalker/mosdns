package shared

import (
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// FileChangeCallback is invoked when a watched file is detected to change.
//
// The filename argument provides the path of the changed file. Implementations
// should perform any necessary reload or rebuild work and return a non-nil
// error when the reload failed. Returned errors are logged by the watcher but
// do not stop the watch loop.
type FileChangeCallback func(filename string) error

// FileWatcher watches a set of files for changes and calls a callback when
// modifications are observed.
//
// It encapsulates an fsnotify watcher, maintains a set of monitored files and
// provides a simple time-based debounce to avoid excessive reloads.
type FileWatcher struct {
	logger       *zap.Logger
	watcher      *fsnotify.Watcher
	files        map[string]struct{}
	cb           FileChangeCallback
	debounce     time.Duration
	lastReloadMu sync.Mutex
	lastReload   time.Time
}

// NewFileWatcher constructs a FileWatcher.
//
// logger: a zap logger used for debug/info/error messages.
// cb: the callback invoked on file changes.
// debounce: minimum interval between successive reloads.
func NewFileWatcher(logger *zap.Logger, cb FileChangeCallback, debounce time.Duration) *FileWatcher {
	return &FileWatcher{
		logger:   logger,
		files:    make(map[string]struct{}),
		cb:       cb,
		debounce: debounce,
	}
}

// Start creates the underlying fsnotify watcher, registers the provided files
// and starts the internal event loop in a goroutine.
//
// If Start is called multiple times the existing watch list will be replaced.
// Start returns an error if fsnotify cannot be initialized or any file cannot
// be added to the watch list.
func (fw *FileWatcher) Start(files []string) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	fw.watcher = w
	// reset files
	fw.files = make(map[string]struct{})
	for _, f := range files {
		if err := w.Add(f); err != nil {
			return err
		}
		fw.files[f] = struct{}{}
		fw.logger.Debug("start watching file", zap.String("file", f))
	}

	go fw.loop()

	return nil
}

// loop is the internal event loop that listens for fsnotify events and
// dispatches reload requests to the configured callback. loop should run in
// its own goroutine and will exit when the underlying watcher channels are
// closed.
func (fw *FileWatcher) loop() {
	fw.lastReloadMu.Lock()
	fw.lastReload = time.Now()
	fw.lastReloadMu.Unlock()
	
	fw.logger.Debug("file watcher loop started")

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				fw.logger.Debug("file watcher closed, exiting loop")
				return
			}

			fw.logger.Debug("received file event", zap.String("event.name", event.Name), zap.String("event.op", event.Op.String()))

			// Check if this is a monitored file
			_, isMonitored := fw.files[event.Name]
			if !isMonitored {
				fw.logger.Debug("ignore event for non-monitored file", zap.String("event", event.Name))
				continue
			}

			// Handle REMOVE and RENAME events that can cause the file to be
			// unwatched. Many editors (vim, nano, etc.) perform atomic file
			// replacements by creating a new file and renaming it over the
			// original, which causes fsnotify to stop watching the file.
			// We need to re-add the file to the watch list.
			if event.Op&fsnotify.Remove == fsnotify.Remove || event.Op&fsnotify.Rename == fsnotify.Rename {
				fw.logger.Info("file removed or renamed, attempting to re-watch", zap.String("file", event.Name))
				// Re-add the file to the watch list after a short delay to allow
				// the file to be recreated (common in atomic replacements)
				go func(filename string) {
					// Wait a bit for the file to be recreated
					time.Sleep(50 * time.Millisecond)
					// Try multiple times with backoff
					for i := 0; i < 5; i++ {
						if err := fw.watcher.Add(filename); err == nil {
							fw.logger.Info("successfully re-added file to watch list", zap.String("file", filename))
							// Trigger a reload since the file was replaced
							fw.lastReloadMu.Lock()
							shouldReload := time.Since(fw.lastReload) >= fw.debounce
							if shouldReload {
								fw.lastReload = time.Now()
							}
							fw.lastReloadMu.Unlock()
							
							if shouldReload {
								start := time.Now()
								if err := fw.cb(filename); err != nil {
									fw.logger.Error("auto-reload failed after file recreation", zap.String("filename", filename), zap.Error(err))
								} else {
									fw.logger.Info("auto-reload completed after file recreation", zap.String("filename", filename), zap.Any("duration", time.Since(start)))
								}
							}
							return
						}
						fw.logger.Debug("failed to re-add file, retrying", zap.String("file", filename), zap.Int("attempt", i+1))
						time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
					}
					fw.logger.Error("failed to re-add file to watch list after multiple attempts", zap.String("file", filename))
				}(event.Name)
				continue
			}

			// Handle CREATE events - re-add the file to watch list in case it
			// was removed and recreated (common with atomic file replacements)
			if event.Op&fsnotify.Create == fsnotify.Create {
				if err := fw.watcher.Add(event.Name); err != nil {
					fw.logger.Error("failed to re-add file to watch list", zap.String("file", event.Name), zap.Error(err))
				} else {
					fw.logger.Debug("re-added file to watch list after create", zap.String("file", event.Name))
				}
			}

			// Trigger reload for Write, Create, or Chmod events
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Chmod == fsnotify.Chmod {
				// simple time-based debounce: skip events that arrive within the
				// configured debounce window since the last reload.
				fw.lastReloadMu.Lock()
				shouldReload := time.Since(fw.lastReload) >= fw.debounce
				if shouldReload {
					fw.lastReload = time.Now()
				}
				fw.lastReloadMu.Unlock()

				if !shouldReload {
					fw.logger.Debug("within debounce period, skipping reload", zap.String("event", event.Name))
					continue
				}

				fw.logger.Debug("file change detected, scheduling reload", zap.String("event", event.Name))

				// invoke the callback asynchronously so the loop keeps receiving
				// events. The callback is responsible for its own synchronization
				// and error handling semantics.
				go func(filename string) {
					start := time.Now()
					if err := fw.cb(filename); err != nil {
						fw.logger.Error("auto-reload failed", zap.String("filename", filename), zap.Any("error", err))
					} else {
						fw.logger.Info("auto-reload completed", zap.String("filename", filename), zap.Any("duration", time.Since(start)))
					}
				}(event.Name)
			}

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				fw.logger.Debug("file watcher closed, exiting loop")
				return
			}
			// Log errors from fsnotify to help diagnose issues
			if err != nil {
				fw.logger.Error("file watcher error", zap.Error(err))
			}
		}
	}
}

// Close stops the file watcher and releases associated resources.
func (fw *FileWatcher) Close() error {
	if fw.watcher != nil {
		fw.logger.Debug("closing file watcher")
		return fw.watcher.Close()
	}
	return nil
}
