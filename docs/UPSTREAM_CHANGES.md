## 上游功能改动与增强（概要）

以下记录了在 mosdns 上游代码中已实现或推荐合入的功能改动与增强，便于提交 PR 或编写变更说明。

1. 文件自动重载（AutoReload）

- 在 `domain_set`、`ip_set`、`hosts` 等数据源中增加了可选的 `auto_reload: true` 配置项。
- 当启用并提供 `files: [...]` 时，程序会监听这些文件的变更并在文件更新后触发对应的 `rebuild` 操作（例如重新构建 Matcher / IP 集合）。

2. 共享 `FileWatcher` 实现

- 将文件监视逻辑抽象为 `plugin/data_provider/shared.FileWatcher`，集中处理 fsnotify 事件、去抖（debounce）、以及对 REMOVE/RENAME/CREATE 场景的重试与 re-watch。
- 关键特性：
  - 去抖：避免短时间重复触发重建（示例默认 500ms）。
  - 处理原子替换：检测到 REMOVE/RENAME 时采用指数退避多次尝试重新 `Add` 文件到 watcher，解决编辑器通过重命名替换文件导致 watcher 中断的问题。
  - CREATE 事件：重新 `Add` 但不立刻触发 reload，等待 WRITE 事件。
  - WRITE/CHMOD：确认文件存在后触发 reload，并异步执行回调以不阻塞事件循环。

3. RouterOS / 地址表集成（`ros_addrlist`）

- 新增或完善了 `ros_addrlist` 插件（示例 tag: `add_gfwlist`），用于将生成的 IP/CIDR 列表同步到 RouterOS(MikroTik) 的 address-list。
- 常用配置项：`addrlist`、`server`、`user`、`passwd`、`mask4`、`mask6`、`dry_run` 等。
- 行为：去重、按 IPv4/IPv6 分区、对 IPv4 进行可选的 CIDR 聚合（减小列表项数），然后通过 API/HTTP/导入脚本同步到 RouterOS。建议支持幂等/差分同步和 dry-run 模式。

4. 测试安全性改进

- 为避免测试修改仓库中示例文件，引入 `outputDir` 参数（或在测试中切换到临时目录），并将测试输出写到 `./tmp/`（并已加入 `.gitignore`）。

5. 建议的后续增强（可选）

- 为 `writeHosts` / 输出生成函数增加 `outputDir` 与 `dry_run` 参数，提升可测试性和可控性。
- `ros_addrlist` 支持 `replace` / `append` / `diff-sync` 策略以适配不同路由器性能与审计需求。
- 凭证使用 Secret 管理（环境变量 / 外部 secret store）以避免配置明文密码。

---

## FileWatcher 行为（技术细节）

- 概念：`FileWatcher` 使用 `fsnotify`，并由 `NewFileWatcher(logger, cb, debounce)` 构造。
- 事件处理策略：
  - 仅对 `Start` 时注册的文件集合内的事件做处理。
  - 对 REMOVE/RENAME：立即记录并在短延迟后用指数退避尝试 `watcher.Add(filename)` 重新监视（5 次尝试，50ms -> 800ms）。
  - 对 CREATE：尝试 `Add`，但不触发 reload；等待 WRITE 事件触发实际 reload。
  - 对 WRITE/CHMOD（且文件存在）：进行 debounce 判断，若超过 debounce 窗口则异步调用回调 `cb(filename)`。
  - 错误处理：将 `fsnotify` 错误写入日志，`cb` 返回的错误也记录但不停止监视循环。

## `ros_addrlist` 插件（建议文档）

- 用途：将 IP 列表写入 RouterOS 的 address-list，通常用于维护 GFW/黑白名单。
- 建议 args：
  - `addrlist` (string) — RouterOS 上的列表名
  - `server` (string) — 管理接口地址或导入 URL
  - `user`/`passwd` (string) — 认证信息（建议使用环境变量/Secret）
  - `mask4` (int) — IPv4 聚合掩码（例如 24）
  - `mask6` (int) — IPv6 聚合掩码（例如 128 或 64）
  - `dry_run` (bool) — 仅打印将要执行的操作
- 行为：去重 -> 聚合（可选）-> 与 RouterOS 同步（支持幂等、差分或替换模式）。

## 配置与环境变量自动覆盖（`AutomaticEnv`）

在 `loadConfig` 中引入了以下两行：

```go
v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
v.AutomaticEnv()
```

说明与影响：

  - 配置键 `plugins.hosts.args.auto_reload` -> 环境变量 `PLUGINS_HOSTS_ARGS_AUTO_RELOAD`
  - 配置键 `plugins.add_gfwlist.args.passwd` -> 环境变量 `PLUGINS_ADD_GFWLIST_ARGS_PASSWD`
 

安全与部署示例：详见 `docs/SECURE_SECRETS.md`，其中包含 Docker Secrets、Kubernetes Secret、Vault 与云 Secret Manager 的用法示例与最佳实践。

- 测试建议：增加配置覆盖测试，验证在设置相应环境变量时，`v.Unmarshal` 后的 `Config` 结构体得到来自环境的值；并在 CI 中展示一个示例（使用 `env` 注入）以避免回归。

## 测试与 CI 建议

- 单元测试：
  - `FileWatcher` 的去抖、rename/re-add、Close 行为。利用临时目录和模拟 fsnotify 事件来断言回调调用次数。
  - `ros_addrlist` 的同步逻辑（使用 HTTP mock 或 RouterOS API mock）。
- 集成测试：在 CI 中运行产生器但把 `outputDir` 定向到 `./tmp`，并断言仓库根目录的示例文件未被修改。
- CI：在 PR 检查中运行 `go test ./...` 与 Node 测试（如有），并阻止修改仓库示例文件被提交。

## 插件环境覆盖：`env_override.go` 的改动说明

- 新增 helper：`applyPluginEnvOverrides(cfg *Config)`（实现文件：`coremain/env_override.go`）。该函数在配置通过 Viper `v.Unmarshal` 反序列化之后运行，用以把进程环境中的 `PLUGINS_*` 变量注入到 `cfg.Plugins[*].Args` 中。

- 支持的环境变量格式（示例）：

  - `PLUGINS_<IDENT>_ARGS_<KEY_PATH>=<value>`

  其中 `<IDENT>` 为插件的 `Tag`（优先）或 `Type`，`<KEY_PATH>` 为下划线分隔的字段路径，会被映射为插件 args 下的嵌套键。例如：

  - `PLUGINS_ADD_GFWLIST_ARGS_PASSWD=secret`
  - `PLUGINS_MYTAG_ARGS_AUTO_RELOAD=true`
  - 用于开发调试的例子：
  ```
  PLUGINS_TCP_SERVER_ARGS_LISTEN=:54 PLUGINS_UDP_SERVER_ARGS_LISTEN=:54 LOG_PRODUCTION=false LOG_LEVEL=debug ./mosdns start -d ../lazymosdns/etc
  ```

- 行为细节：
  - 如果目标插件的 `Args` 为空，helper 会初始化为 `map[string]any` 并写入值。
  - 为兼容不同消费方式，helper 会同时写入“点表示法”嵌套键（例如 `a.b.c`）和下划线连接键（例如 `a_b_c`）。
  - 值解析：会尽可能把字符串解析为布尔、整数或浮点数；解析失败则保留原字符串。

- 局限性与注意事项：
  - 当前实现仅把值写入 `map[string]any`，并不自动将该 map 解码为插件可能期望的强类型 args 结构。若某些插件依赖结构体类型，需要在注入后增加 map→struct 的解码步骤（例如使用 `mapstructure` 或 JSON round-trip）。
  - 仅扫描以 `PLUGINS_` 前缀的环境变量；命名应配合 `v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))` 与 `v.AutomaticEnv()` 使用时的约定。

- 调用位置与测试：该 helper 应在 `loadConfig`（`run.go`）中 v.Unmarshal 之后调用。相关单元测试在 `coremain/run_test.go`（示例：`TestLoadConfig_PluginPasswdEnvOverride`）。

- 与安全实践的关系：对于敏感字段（如 `passwd`），优先使用受管 secret 方案（参见 `docs/SECURE_SECRETS.md`），不要把明文凭证直接写入仓库或 CI 日志。
