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

package hosts

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/IrineSistiana/mosdns/v5/coremain"
	"github.com/IrineSistiana/mosdns/v5/pkg/hosts"
	"github.com/IrineSistiana/mosdns/v5/pkg/matcher/domain"
	"github.com/IrineSistiana/mosdns/v5/pkg/query_context"
	"github.com/IrineSistiana/mosdns/v5/plugin/data_provider/shared"
	"github.com/IrineSistiana/mosdns/v5/plugin/executable/sequence"
	"github.com/miekg/dns"
)

const PluginType = "hosts"

func init() {
	coremain.RegNewPluginFunc(PluginType, Init, func() any { return new(Args) })
}

var _ sequence.Executable = (*Hosts)(nil)

type Args struct {
	Entries    []string `yaml:"entries"`
	Files      []string `yaml:"files"`
	AutoReload bool     `yaml:"auto_reload"`
}

type Hosts struct {
	h    *hosts.Hosts
	bp   *coremain.BP
	fw   *shared.FileWatcher
	args *Args
}

func Init(bp *coremain.BP, args any) (any, error) {
	return NewHosts(bp, args.(*Args))
}

func NewHosts(bp *coremain.BP, args *Args) (*Hosts, error) {
	h := &Hosts{bp: bp, args: args}

	// build initial matcher
	if err := h.rebuild(); err != nil {
		return nil, err
	}

	// optional auto-reload
	if args.AutoReload && len(args.Files) > 0 {
		h.fw = shared.NewFileWatcher(bp.L(), func(filename string) error {
			return h.rebuild()
		}, 500*time.Millisecond)
		if err := h.fw.Start(args.Files); err != nil {
			return nil, fmt.Errorf("failed to start file watcher: %w", err)
		}
	}

	return h, nil
}

// rebuild reconstructs the internal matcher and replaces the wrapped hosts
// instance. It is safe to call concurrently with lookups because the pointer
// swap is atomic.
func (h *Hosts) rebuild() error {
	m := domain.NewMixMatcher[*hosts.IPs]()
	m.SetDefaultMatcher(domain.MatcherFull)
	for i, entry := range h.args.Entries {
		if err := domain.Load(m, entry, hosts.ParseIPs); err != nil {
			return fmt.Errorf("failed to load entry #%d %s, %w", i, entry, err)
		}
	}
	for i, file := range h.args.Files {
		b, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read file #%d %s, %w", i, file, err)
		}
		if err := domain.LoadFromTextReader(m, bytes.NewReader(b), hosts.ParseIPs); err != nil {
			return fmt.Errorf("failed to load file #%d %s, %w", i, file, err)
		}
	}

	h.h = hosts.NewHosts(m)
	return nil
}

func (h *Hosts) Response(q *dns.Msg) *dns.Msg {
	return h.h.LookupMsg(q)
}

func (h *Hosts) Exec(_ context.Context, qCtx *query_context.Context) error {
	r := h.h.LookupMsg(qCtx.Q())
	if r != nil {
		qCtx.SetResponse(r)
	}
	return nil
}

// Close stops the optional file watcher and releases resources. It is safe
// to call multiple times.
func (h *Hosts) Close() error {
	if h.fw != nil {
		return h.fw.Close()
	}
	return nil
}
