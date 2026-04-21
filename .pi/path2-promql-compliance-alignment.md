# Path 2 PromQL compliance alignment

This file is generated from the read-only Prometheus compliance suite at `harness/compliance/prom-compliance/promql/promql-test-queries.yml`.

- Source: `harness/compliance/prom-compliance/promql/promql-test-queries.yml`
- Expanded queries: `539`
- `should_fail` queries: `5`

## Summary

| Path 2 status | Count |
|---|---:|
| `no` | 218 |
| `partial` | 6 |
| `yes` | 315 |

## Unsupported implementation buckets

| Bucket | Count |
|---|---:|
| `binary_expression_gaps` | 67 |
| `quantile_over_time` | 54 |
| `scalar_roots` | 47 |
| `histogram_helpers` | 12 |
| `label_mutation` | 10 |
| `selection_aggregations` | 9 |
| `absent_family` | 8 |
| `expected_error_surface` | 5 |
| `other` | 2 |
| `round_function` | 2 |
| `vector_wrapper` | 2 |

## Partial implementation buckets

| Bucket | Count |
|---|---:|
| `clamp_family` | 6 |

## Unsupported examples

| Bucket | Query | Should fail | Plan reason | Native reason |
|---|---|---|---|---|
| `scalar_roots` | `42` | `false` |  | scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet |
| `scalar_roots` | `1.234` | `false` |  | scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet |
| `scalar_roots` | `.123` | `false` |  | scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet |
| `scalar_roots` | `1.23e-3` | `false` |  | scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet |
| `scalar_roots` | `0x3d` | `false` |  | scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet |
| `scalar_roots` | `Inf` | `false` |  | scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet |
| `scalar_roots` | `+Inf` | `false` |  | scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet |
| `scalar_roots` | `-Inf` | `false` |  | scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet |
| `scalar_roots` | `NaN` | `false` |  | scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet |
| `expected_error_surface` | `{__name__=~".*"}` | `true` |  |  |
| `selection_aggregations` | `topk (3, demo_memory_usage_bytes)` | `false` |  | aggregation operator is not supported by native SQL pushdown |
| `selection_aggregations` | `bottomk (3, demo_memory_usage_bytes)` | `false` |  | aggregation operator is not supported by native SQL pushdown |
| `selection_aggregations` | `topk by(instance) (2, demo_memory_usage_bytes)` | `false` |  | aggregation operator is not supported by native SQL pushdown |
| `selection_aggregations` | `bottomk by(instance) (2, demo_memory_usage_bytes)` | `false` |  | aggregation operator is not supported by native SQL pushdown |
| `selection_aggregations` | `topk without(instance) (2, demo_memory_usage_bytes)` | `false` |  | aggregation operator is not supported by native SQL pushdown |
| `selection_aggregations` | `bottomk without(instance) (2, demo_memory_usage_bytes)` | `false` |  | aggregation operator is not supported by native SQL pushdown |
| `selection_aggregations` | `topk without() (2, demo_memory_usage_bytes)` | `false` |  | aggregation operator is not supported by native SQL pushdown |
| `selection_aggregations` | `bottomk without() (2, demo_memory_usage_bytes)` | `false` |  | aggregation operator is not supported by native SQL pushdown |
| `scalar_roots` | `1 * 2 + 4 / 6 - 10 % 2 ^ 2` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_num_cpus + (1 == bool 2)` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_num_cpus + (1 != bool 2)` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_num_cpus + (1 < bool 2)` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_num_cpus + (1 > bool 2)` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_num_cpus + (1 <= bool 2)` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_num_cpus + (1 >= bool 2)` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes == 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes != 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes < 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes > 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes <= 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes >= 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes == bool 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes != bool 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes < bool 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes > bool 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes <= bool 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `binary_expression_gaps` | `demo_memory_usage_bytes >= bool 1.2345` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `scalar_roots` | `1.2345 == bool demo_memory_usage_bytes` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `scalar_roots` | `1.2345 != bool demo_memory_usage_bytes` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |
| `scalar_roots` | `1.2345 < bool demo_memory_usage_bytes` | `false` |  | binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children |

## Partial examples

| Bucket | Query | Should fail | Plan reason | Native reason |
|---|---|---|---|---|
| `clamp_family` | `clamp_min(demo_memory_usage_bytes, 2)` | `false` |  | clamp_min can lower to a native SQL source expression |
| `clamp_family` | `clamp_max(demo_memory_usage_bytes, 2)` | `false` |  | clamp_max can lower to a native SQL source expression |
| `clamp_family` | `clamp(demo_memory_usage_bytes, 0, 1)` | `false` |  | clamp can lower to a native SQL source expression |
| `clamp_family` | `clamp(demo_memory_usage_bytes, 0, 1000000000000)` | `false` |  | clamp can lower to a native SQL source expression |
| `clamp_family` | `clamp(demo_memory_usage_bytes, 1000000000000, 0)` | `false` |  | clamp can lower to a native SQL source expression |
| `clamp_family` | `clamp(demo_memory_usage_bytes, 1000000000000, 1000000000000)` | `false` |  | clamp can lower to a native SQL source expression |

