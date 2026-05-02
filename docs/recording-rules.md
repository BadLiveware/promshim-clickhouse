# Virtual recording rules

Promshim can expose selected Prometheus recording-rule metric names without
materializing those series in ClickHouse. In `virtual` mode, promshim rewrites an
instant-vector reference to a configured recording rule into that rule's PromQL
expression before planning the query.

This is intended for dashboard and compatibility traffic that expects recording
rule names to exist while ClickHouse remains the source of raw samples.

## Deployment shape

Use the sidecar syncer for Kubernetes deployments that already manage rules as
Prometheus Operator `PrometheusRule` CRDs:

```text
PrometheusRule CRDs -> promshim-rule-syncer -> shared rule files -> promshim
```

- `promshim-rule-syncer` periodically lists selected `PrometheusRule` objects
  and renders Prometheus rule YAML into a shared writable directory.
- promshim reads those rendered YAML files through
  `PROM_SHIM_RECORDING_RULE_FILES`.
- promshim stays Kubernetes-unaware; the syncer owns Kubernetes API access.

A ConfigMap round trip is intentionally not part of this flow. In a sidecar
setup, writing directly to a shared `emptyDir` avoids ConfigMap projection delay
and shard/mount coordination.

## Promshim configuration

Enable virtual rule expansion and point promshim at the shared directory:

```bash
PROM_SHIM_RECORDING_RULE_MODE=virtual
PROM_SHIM_RECORDING_RULE_FILES=/etc/promshim/rules/*.yaml
PROM_SHIM_RECORDING_RULE_RELOAD_INTERVAL_SECONDS=30
```

Reload behavior:

1. promshim loads and validates the configured rule files at startup.
2. every reload interval, promshim re-globs and re-parses the files;
3. if the new registry is valid, promshim atomically swaps it in;
4. if reload fails, promshim logs the error and keeps serving the previous
   registry.

In-flight queries use the registry snapshot they started with. New queries see
the latest valid snapshot.

## Rule syncer configuration

Run `promshim-rule-syncer` in the same pod as promshim and mount the same
writable directory into both containers.

Common flags and environment variables:

| Flag | Env | Default | Meaning |
|---|---|---:|---|
| `--output-dir` | `PROM_SHIM_RULE_SYNC_OUTPUT_DIR` | `/etc/promshim/rules` | Directory for rendered rule YAML files. |
| `--namespaces` | `PROM_SHIM_RULE_SYNC_NAMESPACES` | empty | Comma-separated namespaces to read. Empty means all namespaces. |
| `--rule-selector` | `PROM_SHIM_RULE_SYNC_SELECTOR` | empty | Kubernetes label selector for `PrometheusRule` objects. |
| `--prometheus-version` | `PROM_SHIM_RULE_SYNC_PROMETHEUS_VERSION` | `3.0.0` | Prometheus compatibility version for rule validation. |
| `--sync-interval` | `PROM_SHIM_RULE_SYNC_INTERVAL` | `30s` | Periodic sync interval. |
| `--once` | `PROM_SHIM_RULE_SYNC_ONCE` | `false` | Run one sync and exit. |

The syncer writes each rule file with temp-file plus atomic rename semantics.
Generated files are prefixed with `promshim-`; stale generated `.yaml` files are
removed from the output directory. Other `.yaml` files and non-YAML files are
left alone.

## Minimal Kubernetes sketch

```yaml
securityContext:
  fsGroup: 65532
volumes:
  - name: promshim-rules
    emptyDir: {}
containers:
  - name: promshim
    image: ghcr.io/badliveware/promshim-clickhouse:vX.Y.Z
    env:
      - name: PROM_SHIM_RECORDING_RULE_MODE
        value: virtual
      - name: PROM_SHIM_RECORDING_RULE_FILES
        value: /etc/promshim/rules/*.yaml
      - name: PROM_SHIM_RECORDING_RULE_RELOAD_INTERVAL_SECONDS
        value: "30"
    volumeMounts:
      - name: promshim-rules
        mountPath: /etc/promshim/rules
  - name: promshim-rule-syncer
    image: ghcr.io/badliveware/promshim-clickhouse-rule-syncer:vX.Y.Z
    env:
      - name: PROM_SHIM_RULE_SYNC_NAMESPACES
        value: observability
      - name: PROM_SHIM_RULE_SYNC_SELECTOR
        value: release=k8s-monitoring
      - name: PROM_SHIM_RULE_SYNC_OUTPUT_DIR
        value: /etc/promshim/rules
    volumeMounts:
      - name: promshim-rules
        mountPath: /etc/promshim/rules
```

The syncer container runs as the distroless nonroot user. Set a pod or volume
security context, such as `fsGroup: 65532`, so it can write to the shared
`emptyDir`.

The syncer needs RBAC to list `monitoring.coreos.com/v1` `prometheusrules` in
the selected namespaces. If promshim starts before the sidecar has written any
matching files, it starts with an empty virtual-rule registry and picks up files
on the next successful reload.

## Query semantics

Supported:

- `/api/v1/query` instant-vector contexts;
- `/api/v1/query_range` re-evaluation of instant expressions at each step;
- recording-rule labels and group labels on the virtual result expression;
- live reload of rendered rule files with keep-last-good behavior.

Not supported in the MVP:

- range selectors over virtual recording rules, such as
  `my_recording_rule[5m]`;
- alerting-rule evaluation;
- materializing rule output series;
- Prometheus rule scheduling semantics, missed evaluations, or rule state.

Range selectors over virtual rules return an error because they require either
materialized history or bounded virtual-history semantics.

## Release artifacts

The main promshim release tag publishes both containers:

- `ghcr.io/badliveware/promshim-clickhouse:vX.Y.Z`
- `ghcr.io/badliveware/promshim-clickhouse-rule-syncer:vX.Y.Z`

The downloadable archive also includes both binaries for the tagged platform.
