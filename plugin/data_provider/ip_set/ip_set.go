/*
 * Copyright (C) 2020-2022, IrineSistiana
 *
 * This file is part of mosdns.
 *
 * mosdns is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * mosdns is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package ip_set

import (
	"bytes"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/IrineSistiana/mosdns/v5/coremain"
	"github.com/IrineSistiana/mosdns/v5/pkg/matcher/netlist"
	"github.com/IrineSistiana/mosdns/v5/plugin/data_provider"
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

const PluginType = "ip_set"

func init() {
	coremain.RegNewPluginFunc(PluginType, Init, func() any { return new(Args) })
}

func Init(bp *coremain.BP, args any) (any, error) {
	logger = bp.L()
	return NewIPSet(bp, args.(*Args))
}

type Args struct {
	IPs        []string `yaml:"ips"`
	Sets       []string `yaml:"sets"`
	Files      []string `yaml:"files"`
	AutoReload bool     `yaml:"auto_reload"`
}

var (
	_      data_provider.IPMatcherProvider = (*IPSet)(nil)
	logger                                 = (*zap.Logger)(nil)
)

type IPSet struct {
	mg []netlist.Matcher

	// 可选的热加载支持
	watcher *fsnotify.Watcher
	files   []string
	// 重建参数
	bp   *coremain.BP
	args *Args
}

func (p *IPSet) GetIPMatcher() netlist.Matcher {
	return MatcherGroup(p.mg)
}

// rebuildMatcher 重建matcher
func (p *IPSet) rebuildMatcher() error {
	logger.Debug("开始重建netlist matcher")

	l := netlist.NewList()
	if err := LoadFromIPsAndFiles(p.args.IPs, p.args.Files, l); err != nil {
		return fmt.Errorf("加载文件失败: %v", err)
	}
	l.Sort()
	if l.Len() > 0 {
		p.mg = append(p.mg, l)
		logger.Info("成功加载IP表和文件", zap.Int("IPs", len(p.args.IPs)), zap.Int("files", len(p.args.Files)),
			zap.Int("netlist", l.Len()))
	}
	for _, tag := range p.args.Sets {
		provider, _ := p.bp.M().GetPlugin(tag).(data_provider.IPMatcherProvider)
		if provider == nil {
			return fmt.Errorf("%s is not an IPMatcherProvider", tag)
		}
		p.mg = append(p.mg, provider.GetIPMatcher())
		logger.Info("成功添加插件", zap.String("matcher", tag))
	}

	return nil
}

func NewIPSet(bp *coremain.BP, args *Args) (*IPSet, error) {
	p := &IPSet{
		bp:   bp,
		args: args,
	}

	// 构建初始matcher
	if err := p.rebuildMatcher(); err != nil {
		return nil, err
	}

	// 可选的文件监控
	logger.Info("文件热重载功能状态", zap.Bool("auto_reload", args.AutoReload), zap.Any("files", args.Files))
	if args.AutoReload && len(args.Files) > 0 {
		p.files = args.Files
		if err := p.startFileWatcher(); err != nil {
			logger.Sugar().Errorf("启动文件监控失败: %v", err)
			return nil, fmt.Errorf("failed to start file watcher: %w", err)
		}
		logger.Info("文件监控启动成功")
	}

	return p, nil
}

func parseNetipPrefix(s string) (netip.Prefix, error) {
	if strings.ContainsRune(s, '/') {
		return netip.ParsePrefix(s)
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return addr.Prefix(addr.BitLen())
}

func LoadFromIPsAndFiles(ips []string, fs []string, l *netlist.List) error {
	if err := LoadFromIPs(ips, l); err != nil {
		return err
	}
	if err := LoadFromFiles(fs, l); err != nil {
		return err
	}
	return nil
}

func LoadFromIPs(ips []string, l *netlist.List) error {
	for i, s := range ips {
		p, err := parseNetipPrefix(s)
		if err != nil {
			return fmt.Errorf("invalid ip #%d %s, %w", i, s, err)
		}
		l.Append(p)
	}
	return nil
}

func LoadFromFiles(fs []string, l *netlist.List) error {
	for i, f := range fs {
		if err := LoadFromFile(f, l); err != nil {
			return fmt.Errorf("failed to load file #%d %s, %w", i, f, err)
		}
	}
	return nil
}

func LoadFromFile(f string, l *netlist.List) error {
	if len(f) > 0 {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := netlist.LoadFromReader(l, bytes.NewReader(b)); err != nil {
			return err
		}
	}
	return nil
}

type MatcherGroup []netlist.Matcher

func (mg MatcherGroup) Match(addr netip.Addr) bool {
	for _, m := range mg {
		if m.Match(addr) {
			return true
		}
	}
	return false
}

func (p *IPSet) startFileWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	p.watcher = watcher

	for _, file := range p.files {
		if err := watcher.Add(file); err != nil {
			return fmt.Errorf("failed to watch file %s: %w", file, err)
		}
		logger.Debug("开始监控文件", zap.String("file", file))
	}

	go p.watchFiles()

	return nil
}

func (p *IPSet) watchFiles() {
	lastReload := time.Now()

	logger.Debug("文件监控循环开始")

	for {
		select {
		case event, ok := <-p.watcher.Events:
			if !ok {
				logger.Debug("文件监控已关闭，退出监控循环")
				return
			}

			logger.Debug("收到文件事件", zap.String("event.name", event.Name), zap.String("event.op", event.Op.String()))

			if event.Op&fsnotify.Write == fsnotify.Write ||
				event.Op&fsnotify.Create == fsnotify.Create {

				// 检查是否是监控的文件
				monitored := false
				for _, file := range p.files {
					if file == event.Name {
						monitored = true
						break
					}
				}

				if !monitored {
					logger.Debug("忽略非监控文件的事件", zap.String("event", event.Name))
					continue
				}

				// 简单防抖
				if time.Since(lastReload) < 500*time.Millisecond {
					logger.Debug("防抖期内，跳过重载", zap.String("event", event.Name))
					continue
				}

				logger.Debug("检测到文件变更，开始热重载", zap.String("event", event.Name))

				// 异步重载，不阻塞
				go func(filename string) {
					start := time.Now()
					if err := p.rebuildMatcher(); err != nil {
						logger.Error("热重载失败", zap.String("filename", filename), zap.Any("duration", err))
					} else {
						logger.Info("热重载完成", zap.String("filename", filename), zap.Any("duration", time.Since(start)))
					}
				}(event.Name)

				lastReload = time.Now()
			}

		case _, ok := <-p.watcher.Errors:
			if !ok {
				logger.Debug("文件监控已关闭，退出监控循环")
				return
			}
		}
	}
}

func (p *IPSet) Close() error {
	if p.watcher != nil {
		logger.Info("关闭文件监控器")
		return p.watcher.Close()
	}
	return nil
}
