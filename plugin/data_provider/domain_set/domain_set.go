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

package domain_set

import (
	"bytes"
	"fmt"
	"time"

	"os"

	"github.com/IrineSistiana/mosdns/v5/coremain"
	"github.com/IrineSistiana/mosdns/v5/pkg/matcher/domain"
	"github.com/IrineSistiana/mosdns/v5/plugin/data_provider"
	"github.com/IrineSistiana/mosdns/v5/plugin/data_provider/shared"
	"go.uber.org/zap"
)

const PluginType = "domain_set"

func init() {
	coremain.RegNewPluginFunc(PluginType, Init, func() any { return new(Args) })
}

func Init(bp *coremain.BP, args any) (any, error) {
	logger = bp.L()
	m, err := NewDomainSet(bp, args.(*Args))
	if err != nil {
		return nil, err
	}
	return m, nil
}

type Args struct {
	Exps       []string `yaml:"exps"`
	Sets       []string `yaml:"sets"`
	Files      []string `yaml:"files"`
	AutoReload bool     `yaml:"auto_reload"`
}

var (
	_      data_provider.DomainMatcherProvider = (*DomainSet)(nil)
	logger                                     = (*zap.Logger)(nil)
)

type DomainSet struct {
	// dynamic matcher group, supports auto reload
	dynamicGroup *DynamicMatcherGroup

	fw *shared.FileWatcher

	// rebuild parameters
	bp   *coremain.BP
	args *Args
}

// GetDomainMatcher returns the dynamic matcher that performs domain matching.
//
// The matcher is updated by rebuild operations and is safe to call
// concurrently from multiple goroutines.
func (d *DomainSet) GetDomainMatcher() domain.Matcher[struct{}] {
	return d.dynamicGroup
}

// rebuildMatcher rebuilds the internal matcher group from configured
// expressions, files and referenced matcher plugins.
//
// This method is invoked during initialization and by file-change callbacks
// when auto-reload is enabled. It returns an error if any load operation
// fails.
func (d *DomainSet) rebuildMatcher() error {
	logger.Debug("start rebuilding domain matcher")

	var matchers []domain.Matcher[struct{}]

	// If expressions or files are configured, load them into a MixMatcher and
	// include it in the matcher list.
	if len(d.args.Exps) > 0 || len(d.args.Files) > 0 {
		m := domain.NewDomainMixMatcher()
		if err := LoadExpsAndFiles(d.args.Exps, d.args.Files, m); err != nil {
			return fmt.Errorf("failed to load exprs and files: %v", err)
		}
		if m.Len() > 0 {
			matchers = append(matchers, m)
			logger.Info("successfully loaded exprs and files", zap.Int("exps", len(d.args.Exps)), zap.Int("files", len(d.args.Files)),
				zap.Int("domains", m.Len()))
		}
	}

	// Add matchers provided by other plugins listed in args.Sets.
	for _, tag := range d.args.Sets {
		provider, _ := d.bp.M().GetPlugin(tag).(data_provider.DomainMatcherProvider)
		if provider == nil {
			return fmt.Errorf("%s is not a DomainMatcherProvider", tag)
		}
		matcher := provider.GetDomainMatcher()
		matchers = append(matchers, matcher)
		logger.Info("successfully added plugin", zap.String("matcher", tag))
	}

	// Update the dynamic matcher group atomically.
	newGroup := MatcherGroup(matchers)
	d.dynamicGroup.Update(newGroup)

	logger.Info("domain matcher rebuild complete", zap.Int("matchers", len(matchers)), zap.Any("matcher_details", matchers))

	return nil
}

// NewDomainSet constructs a DomainSet, builds its initial matcher state and
// optionally starts the file watcher for auto-reload when enabled in args.
func NewDomainSet(bp *coremain.BP, args *Args) (*DomainSet, error) {
	ds := &DomainSet{
		bp:           bp,
		args:         args,
		dynamicGroup: NewDynamicMatcherGroup(),
	}

	// build initial matcher
	if err := ds.rebuildMatcher(); err != nil {
		return nil, err
	}

	// optional file watcher
	logger.Info("file auto-reload status", zap.Bool("auto_reload", args.AutoReload), zap.Any("files", args.Files))
	if args.AutoReload && len(args.Files) > 0 {
		logger.Info("enabling file auto-reload", zap.Any("files", args.Files))
		ds.fw = shared.NewFileWatcher(logger, func(filename string) error {
			return ds.rebuildMatcher()
		}, 500*time.Millisecond)
		if err := ds.fw.Start(args.Files); err != nil {
			return nil, fmt.Errorf("failed to start file watcher: %w", err)
		}
		logger.Info("file watcher started successfully")
	}

	return ds, nil
}

// LoadExpsAndFiles loads both expressions and files into the provided
// MixMatcher. It returns early on the first error encountered.
func LoadExpsAndFiles(exps []string, fs []string, m *domain.MixMatcher[struct{}]) error {
	if err := LoadExps(exps, m); err != nil {
		return err
	}
	if err := LoadFiles(fs, m); err != nil {
		return err
	}
	return nil
}

// LoadExps adds the provided domain expressions to the MixMatcher. Returns
// an error that identifies which expression failed if any.
func LoadExps(exps []string, m *domain.MixMatcher[struct{}]) error {
	for i, exp := range exps {
		if err := m.Add(exp, struct{}{}); err != nil {
			return fmt.Errorf("failed to load expression #%d %s, %w", i, exp, err)
		}
	}
	return nil
}

// LoadFiles loads domain entries from each file in fs into the MixMatcher.
// Errors include the index and filename to ease debugging.
func LoadFiles(fs []string, m *domain.MixMatcher[struct{}]) error {
	for i, f := range fs {
		if err := LoadFile(f, m); err != nil {
			return fmt.Errorf("failed to load file #%d %s, %w", i, f, err)
		}
	}
	return nil
}

// LoadFile reads domains from a single file and appends them to the MixMatcher.
// If f is empty the function returns nil.
func LoadFile(f string, m *domain.MixMatcher[struct{}]) error {
	if len(f) > 0 {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}

		if err := domain.LoadFromTextReader(m, bytes.NewReader(b), nil); err != nil {
			return err
		}
	}
	return nil
}

// Close stops the file watcher (if enabled) and releases resources. It is
// safe to call multiple times.
func (d *DomainSet) Close() error {
	if d.fw != nil {
		return d.fw.Close()
	}
	return nil
}
