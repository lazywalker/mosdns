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

package mlog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func readLastJSONLine(path string) (map[string]interface{}, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if ln == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(ln), &m); err == nil {
			return m, nil
		}
	}
	return nil, nil
}

func TestTimeFormatEncodings(t *testing.T) {
	cases := []struct {
		name  string
		tf    string
		check func(t *testing.T, v interface{})
	}{
		{
			name: "timestamp (default)",
			tf:   "timestamp",
			check: func(t *testing.T, v interface{}) {
				if _, ok := v.(float64); !ok {
					t.Fatalf("expected numeric ts, got %T (%v)", v, v)
				}
			},
		},
		{
			name: "iso8601",
			tf:   "iso8601",
			check: func(t *testing.T, v interface{}) {
				s, ok := v.(string)
				if !ok {
					t.Fatalf("expected string ts, got %T (%v)", v, v)
				}
				if _, err := time.Parse(time.RFC3339, s); err != nil {
					// allow a couple of ISO variants with timezone formats
					if _, err2 := time.Parse("2006-01-02T15:04:05.000Z0700", s); err2 != nil {
						t.Fatalf("iso8601 ts not parseable: %v (%v, %v)", s, err, err2)
					}
				}
			},
		},
		{
			name: "rfc3339",
			tf:   "rfc3339",
			check: func(t *testing.T, v interface{}) {
				s, ok := v.(string)
				if !ok {
					t.Fatalf("expected string ts, got %T (%v)", v, v)
				}
				if _, err := time.Parse(time.RFC3339, s); err != nil {
					t.Fatalf("rfc3339 ts not parseable: %v (%v)", s, err)
				}
			},
		},
		{
			name: "custom layout",
			tf:   "custom:2006-01-02 15:04:05",
			check: func(t *testing.T, v interface{}) {
				s, ok := v.(string)
				if !ok {
					t.Fatalf("expected string ts, got %T (%v)", v, v)
				}
				if _, err := time.Parse("2006-01-02 15:04:05", s); err != nil {
					t.Fatalf("custom ts not parseable: %v (%v)", s, err)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "logtest-*.log")
			if err != nil {
				t.Fatalf("tempfile: %v", err)
			}
			path := f.Name()
			f.Close()
			defer os.Remove(path)

			lc := LogConfig{Level: "info", File: path, Production: true, TimeFormat: c.tf}
			lg, err := NewLogger(lc)
			if err != nil {
				t.Fatalf("NewLogger: %v", err)
			}
			lg.Info("test-entry", zap.String("k", "v"))
			_ = lg.Sync()

			m, err := readLastJSONLine(path)
			if err != nil {
				t.Fatalf("read log: %v", err)
			}
			if m == nil {
				t.Fatalf("no JSON log line found in %s", path)
			}
			tsv, ok := m["ts"]
			if !ok {
				t.Fatalf("ts field missing in log: %+v", m)
			}
			c.check(t, tsv)
		})
	}
}
