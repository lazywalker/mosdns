package shared

import (
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
	logger   *zap.Logger
	watcher  *fsnotify.Watcher
	files    map[string]struct{}
	cb       FileChangeCallback
	debounce time.Duration
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
	lastReload := time.Now()
	fw.logger.Debug("file watcher loop started")

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				fw.logger.Debug("file watcher closed, exiting loop")
				return
			}

			fw.logger.Debug("received file event", zap.String("event.name", event.Name), zap.String("event.op", event.Op.String()))

			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				if _, ok := fw.files[event.Name]; !ok {
					fw.logger.Debug("ignore event for non-monitored file", zap.String("event", event.Name))
					continue
				}

				// simple time-based debounce: skip events that arrive within the
				// configured debounce window since the last reload.
				if time.Since(lastReload) < fw.debounce {
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

				lastReload = time.Now()
			}

		case _, ok := <-fw.watcher.Errors:
			if !ok {
				fw.logger.Debug("file watcher closed, exiting loop")
				return
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
