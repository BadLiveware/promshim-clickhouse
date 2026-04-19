# ch-observability-cnpg

Private helper chart for provisioned PostgreSQL via CloudNativePG in this PoC.

## Deploy

```bash
helm template ch-observability-cnpg ./chart/ch-observability-cnpg --namespace monitoring-v2 |
  kubectl --context kind-ch-observability-poc apply --server-side --field-manager=ch-observability-bootstrap --force-conflicts -n monitoring-v2 -f -
```
