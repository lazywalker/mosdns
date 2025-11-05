package shared

import (
    "time"

    "github.com/fsnotify/fsnotify"
    "go.uber.org/zap"
)

// FileChangeCallback is called when a watched file changes. The filename is
// provided for logging; callback may ignore it. Return an error to let the
// watcher log failures.
type FileChangeCallback func(filename string) error

type FileWatcher struct {
    logger   *zap.Logger
    watcher  *fsnotify.Watcher
    files    map[string]struct{}
    cb       FileChangeCallback
    debounce time.Duration
}

func NewFileWatcher(logger *zap.Logger, cb FileChangeCallback, debounce time.Duration) *FileWatcher {
    return &FileWatcher{
        logger:   logger,
        files:    make(map[string]struct{}),
        cb:       cb,
        debounce: debounce,
    }
}

// Start will create the underlying fsnotify watcher, add the provided files
// and start the event loop. If Start is called multiple times it will replace
// the watch list.
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
        fw.logger.Debug("开始监控文件", zap.String("file", f))
    }

    go fw.loop()

    return nil
}

func (fw *FileWatcher) loop() {
    lastReload := time.Now()
    fw.logger.Debug("文件监控循环开始")

    for {
        select {
        case event, ok := <-fw.watcher.Events:
            if !ok {
                fw.logger.Debug("文件监控已关闭，退出监控循环")
                return
            }

            fw.logger.Debug("收到文件事件", zap.String("event.name", event.Name), zap.String("event.op", event.Op.String()))

            if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
                if _, ok := fw.files[event.Name]; !ok {
                    fw.logger.Debug("忽略非监控文件的事件", zap.String("event", event.Name))
                    continue
                }

                if time.Since(lastReload) < fw.debounce {
                    fw.logger.Debug("防抖期内，跳过重载", zap.String("event", event.Name))
                    continue
                }

                fw.logger.Debug("检测到文件变更，开始热重载", zap.String("event", event.Name))

                // call callback asynchronously
                go func(filename string) {
                    start := time.Now()
                    if err := fw.cb(filename); err != nil {
                        fw.logger.Error("热重载失败", zap.String("filename", filename), zap.Any("error", err))
                    } else {
                        fw.logger.Info("热重载完成", zap.String("filename", filename), zap.Any("duration", time.Since(start)))
                    }
                }(event.Name)

                lastReload = time.Now()
            }

        case _, ok := <-fw.watcher.Errors:
            if !ok {
                fw.logger.Debug("文件监控已关闭，退出监控循环")
                return
            }
        }
    }
}

func (fw *FileWatcher) Close() error {
    if fw.watcher != nil {
        fw.logger.Info("关闭文件监控器")
        return fw.watcher.Close()
    }
    return nil
}

