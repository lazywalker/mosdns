````markdown
## 安全的凭证传递（通过环境与 Secret 管理）

敏感字段（例如插件的 `passwd`）可以通过环境变量注入以便在容器化或云环境中动态设置。但直接把明文密码写入仓库或 CI 日志会造成泄漏风险。下面给出安全传递凭证的推荐做法与示例。

- **推荐原则**:
  - 避免将明文凭证提交到版本控制。把密钥/密码放到受控的 secret 存储中（Kubernetes Secret、Docker Secrets、HashiCorp Vault、AWS Secrets Manager 等）。
  - 最小权限与短期凭证：为访问 secret 的服务授予最小权限并尽可能使用短期/自动轮换的凭证。
  - 不要在 CI 日志/公开输出打印凭证；在 CI 中请使用受管的 secret 注入功能（例如 GitHub Actions Secrets 或自托管的 secret provider）。
  - 若使用文件挂载（`/run/secrets/...`），在容器启动脚本中从文件读取并导出到环境变量，避免在镜像或仓库中留下敏感信息。

- **环境变量命名约定**

  配置覆盖支持的环境变量命名约定示例：

  - 配置键 `plugins.add_gfwlist.args.passwd` -> 环境变量 `PLUGINS_ADD_GFWLIST_ARGS_PASSWD`
  - 配置键 `plugins.hosts.args.auto_reload` -> 环境变量 `PLUGINS_HOSTS_ARGS_AUTO_RELOAD`

  例如要为 `ros_addrlist` / `add_gfwlist` 插件注入密码：

  ## 安全的凭证传递（通过环境与 Secret 管理）

  敏感字段（例如插件的 `passwd`）可以通过环境变量注入以便在容器化或云环境中动态设置。但直接把明文密码写入仓库或 CI 日志会造成泄漏风险。下面给出安全传递凭证的推荐做法与可复制示例。

  ### 推荐原则

  - 避免将明文凭证提交到版本控制；使用受控的 secret 存储（Kubernetes Secret、Docker Secrets、HashiCorp Vault、AWS Secrets Manager 等）。
  - 采用最小权限与短期凭证策略；优先使用可轮换 / 自动失效的凭证。
  - 在 CI 中使用受管 secret 注入（例如 GitHub Actions Secrets、GitLab CI Variables），不要把凭证打印到日志。
  - 若使用文件挂载（例如 `/run/secrets/...`），在容器入口脚本中读取并导出到环境变量，避免把凭证写入镜像或仓库。

  ### 环境变量命名约定

  使用 Viper 的 `AutomaticEnv`（并把 `.` 替换为 `_`）后，推荐的命名：

  - 配置键 `plugins.add_gfwlist.args.passwd` -> 环境变量 `PLUGINS_ADD_GFWLIST_ARGS_PASSWD`
  - 配置键 `plugins.hosts.args.auto_reload` -> 环境变量 `PLUGINS_HOSTS_ARGS_AUTO_RELOAD`

  示例（直接导出到当前 shell — 有泄露风险，仅作演示）：

  ```bash
  export PLUGINS_ADD_GFWLIST_ARGS_PASSWD='s3cr3t'
  ./mosdns
  ```

  更安全的做法见下方示例（Docker Secrets / Kubernetes / Vault / AWS）。

  ---

  ### Docker Compose / Docker Secrets（示例）

  将凭证以 secret 文件形式提供并挂载到容器（Docker Compose v3+）：

  ```yaml
  version: '3.7'
  services:
    mosdns:
      image: lazywalker/mosdns
      secrets:
        - mosdns_passwd

  secrets:
    mosdns_passwd:
      file: ./secrets/mosdns_passwd
  ```

  在容器入口脚本（`entrypoint.sh`）中读取并导出：

  ```sh
  if [ -f /run/secrets/mosdns_passwd ]; then
    export PLUGINS_ADD_GFWLIST_ARGS_PASSWD=$(cat /run/secrets/mosdns_passwd)
  fi
  exec "$@"
  ```

  ---

  ### Kubernetes（示例）

  先创建 Secret（注意 `data` 要用 base64 编码）：

  ```yaml
  apiVersion: v1
  kind: Secret
  metadata:
    name: mosdns-secrets
  type: Opaque
  data:
    add-gfwlist-passwd: <base64-encoded-password>
  ```

  在 `Deployment` 中把 Secret 映射为环境变量：

  ```yaml
  apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: mosdns
  spec:
    template:
      spec:
        containers:
        - name: mosdns
          image: lazywalker/mosdns
          env:
          - name: PLUGINS_ADD_GFWLIST_ARGS_PASSWD
            valueFrom:
              secretKeyRef:
                name: mosdns-secrets
                key: add-gfwlist-passwd
  ```

  这种方式结合 Kubernetes 的 RBAC 与 secret 生命周期管理可以降低泄露风险。

  ---

  ### HashiCorp Vault（示例思路）

  推荐通过 Vault Agent 或 Vault CSI 驱动把 secret 渲染为容器内文件，然后在入口脚本中读取：

  示例模板（Vault Agent，伪代码）：

  ```hcl
  template "secrets.hcl" {
    source      = "/vault/templates/mosdns.tpl"
    destination = "/etc/mosdns/secrets/mosdns_passwd"
  }
  ```

  入口脚本读取 `/etc/mosdns/secrets/mosdns_passwd` 并导出为环境变量（同 Docker Secrets 示例）。Vault 提供审计、动态凭证和自动轮换功能。

  ---

  ### AWS Secrets Manager / ECS（示例）

  在 ECS Task Definition 中可以直接把 Secrets Manager 的 secret 映射为环境变量：

  ```json
  "secrets": [
    {
      "name": "PLUGINS_ADD_GFWLIST_ARGS_PASSWD",
      "valueFrom": "arn:aws:secretsmanager:...:secret:mosdns-add-gfwlist-passwd"
    }
  ]
  ```

  在 EKS 中也可以使用 AWS Secrets and Config Provider（ASCP）把 secrets 注入 Pod。

  ---

  ### 其他安全注意事项

  - 在 CI 中使用受管 secret 注入功能并遮蔽日志中的敏感输出。
  - 避免将凭证写入镜像或仓库；对凭证进行轮换并保持审计记录。
  - 优先使用文件挂载 / secret-provider 插件以减少凭证在进程环境中被读取的风险（视平台而定）。

  ---

  以上示例旨在为不同部署环境提供可复制的安全实践，帮助你为 `mosdns` 插件（例如 `ros_addrlist`）安全地注入凭证。
          - mosdns_passwd
