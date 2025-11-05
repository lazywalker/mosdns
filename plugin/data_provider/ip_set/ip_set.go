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
	"github.com/IrineSistiana/mosdns/v5/plugin/data_provider/shared"
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

// IPSet provides an IP matcher composed from configured IPs, files and
// referenced matcher plugins. It optionally supports auto-reloading when a
// FileWatcher is enabled.
type IPSet struct {
	mg []netlist.Matcher

	// optional auto-reload support
	fw *shared.FileWatcher
	// rebuild parameters
	bp   *coremain.BP
	args *Args
}

// GetIPMatcher returns a netlist.Matcher that implements IP matching using
// the internal matcher group. The returned matcher may be used concurrently.
func (p *IPSet) GetIPMatcher() netlist.Matcher {
	return MatcherGroup(p.mg)
}

// rebuildMatcher reconstructs the internal netlist matcher from configured
// IP prefixes, files and referenced matcher plugins. It returns an error
// if any of the load operations fail.
func (p *IPSet) rebuildMatcher() error {
	logger.Debug("start rebuilding netlist matcher")

	l := netlist.NewList()
	if err := LoadFromIPsAndFiles(p.args.IPs, p.args.Files, l); err != nil {
		return fmt.Errorf("failed to load files: %v", err)
	}
	l.Sort()
	if l.Len() > 0 {
		p.mg = append(p.mg, l)
		logger.Info("successfully loaded IPs and files", zap.Int("IPs", len(p.args.IPs)), zap.Int("files", len(p.args.Files)),
			zap.Int("netlist", l.Len()))
	}
	for _, tag := range p.args.Sets {
		provider, _ := p.bp.M().GetPlugin(tag).(data_provider.IPMatcherProvider)
		if provider == nil {
			return fmt.Errorf("%s is not an IPMatcherProvider", tag)
		}
		p.mg = append(p.mg, provider.GetIPMatcher())
		logger.Info("successfully added plugin", zap.String("matcher", tag))
	}

	return nil
}

// NewIPSet creates a new IPSet, builds the initial matcher state and
// optionally starts the file watcher to support auto-reload.
func NewIPSet(bp *coremain.BP, args *Args) (*IPSet, error) {
	p := &IPSet{
		bp:   bp,
		args: args,
	}

	// build initial matcher
	if err := p.rebuildMatcher(); err != nil {
		return nil, err
	}

	// optional file watcher
	logger.Info("file auto-reload status", zap.Bool("auto_reload", args.AutoReload), zap.Any("files", args.Files))
	if args.AutoReload && len(args.Files) > 0 {
		p.fw = shared.NewFileWatcher(logger, func(filename string) error {
			return p.rebuildMatcher()
		}, 500*time.Millisecond)
		if err := p.fw.Start(args.Files); err != nil {
			logger.Sugar().Errorf("failed to start file watcher: %v", err)
			return nil, fmt.Errorf("failed to start file watcher: %w", err)
		}
		logger.Info("file watcher started successfully")
	}

	return p, nil
}

// parseNetipPrefix parses a string that represents either an IP address or a
// CIDR-style prefix and returns a netip.Prefix. If the input contains a
// '/' it is treated as a prefix; otherwise it is parsed as an address and a
// full-length prefix is returned.
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

// LoadFromIPsAndFiles loads IP prefixes from both the ips slice and the
// provided files into the target netlist.List. It returns early on first
// error.
func LoadFromIPsAndFiles(ips []string, fs []string, l *netlist.List) error {
	if err := LoadFromIPs(ips, l); err != nil {
		return err
	}
	if err := LoadFromFiles(fs, l); err != nil {
		return err
	}
	return nil
}

// LoadFromIPs parses the ips slice and appends valid prefixes to the
// netlist.List. The returned error includes the index of the failing entry.
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

// LoadFromFiles loads prefixes from each of the provided files into the
// netlist.List. Errors identify the failing file by index and name.
func LoadFromFiles(fs []string, l *netlist.List) error {
	for i, f := range fs {
		if err := LoadFromFile(f, l); err != nil {
			return fmt.Errorf("failed to load file #%d %s, %w", i, f, err)
		}
	}
	return nil
}

// LoadFromFile reads a list of prefixes from a single file and loads them
// into the given netlist.List. If f is empty the function is a no-op.
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

// MatcherGroup is a helper that composes multiple netlist.Matcher
// implementations and returns true if any of them match the given address.
type MatcherGroup []netlist.Matcher

func (mg MatcherGroup) Match(addr netip.Addr) bool {
	for _, m := range mg {
		if m.Match(addr) {
			return true
		}
	}
	return false
}

// Close stops the optional file watcher and releases resources.
func (p *IPSet) Close() error {
	if p.fw != nil {
		return p.fw.Close()
	}
	return nil
}
