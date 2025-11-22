# Logging configuration (`time_format`)

This document describes the `time_format` option added to the `LogConfig`
used by mosdns' logger (`mlog`). It controls how the `ts` timestamp
field is encoded in structured (JSON) logs and how time is displayed in
console (development) logs.

Location
- Go struct: `mlog.LogConfig` (`time_format` YAML tag)
- Logger implementation: `mlog/logger.go`

Supported values
- `timestamp` (default): numeric epoch timestamp (existing behaviour).
- `iso8601`: ISO8601 human-readable timestamps, e.g. `2025-11-22T14:15:26.048Z`.
- `rfc3339`: RFC3339 timestamps (equivalent to Go's `time.RFC3339`).
- `custom:<layout>`: use a custom Go time layout after the `custom:`
  prefix. Example: `custom:2006-01-02 15:04:05` produces `2025-11-22 14:15:26`.

Examples (YAML)

1) Keep numeric timestamp (default):

```yaml
log:
  level: info
  production: true
  time_format: timestamp
```

2) Use ISO8601 for human-readable timestamps:

```yaml
log:
  level: info
  production: true
  time_format: iso8601
```

3) Custom layout example:

```yaml
log:
  level: debug
  production: true
  time_format: "custom:2006-01-02 15:04:05"
```

Notes
- The change affects both the production JSON encoder and the
  development console encoder so local and structured logs can be
  aligned.
- The code includes unit tests (`mlog/logger_test.go`) that verify the
  `ts` field formatting for `timestamp`, `iso8601`, `rfc3339`, and a
  sample custom layout.
- For sensitive environments, prefer shipping structured logs to a
  centralized logging backend rather than printing secrets in logs.
