package main

import (
  "fmt"
  "time"
  n "ch-observability/internal/promshim/native"
  "ch-observability/internal/promshim/storage"
)

func fragment(fn string) *n.NativeFragment {
  return &n.NativeFragment{
    Kind: n.FragmentKindRangeFunction,
    OutputKind: n.OutputKindInstantVector,
    RangeFunction: &n.RangeFunctionFragment{
      Func: fn,
      Child: &n.NativeFragment{
        Kind: n.FragmentKindSubquery,
        OutputKind: n.OutputKindRangeMatrix,
        Subquery: &n.SubqueryFragment{
          Range: 5 * time.Minute,
          Step:  time.Minute,
          Child: &n.NativeFragment{
            Kind:       n.FragmentKindBinaryScalarSourceExpr,
            OutputKind: n.OutputKindInstantVector,
            Selector:   &n.SelectorSource{Kind: n.SelectorKindInstantVector, MetricName: "harness_up", Lookback: 5 * time.Minute},
            ValueExpr:  "({value}) * 100",
            TagsExpr:   "{tags}",
          },
        },
      },
    },
  }
}

func main() {
  for _, fn := range []string{"last_over_time", "sum_over_time"} {
    rendered, err := n.RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment(fn), n.RenderParams{
      Mode:            n.RenderModeRange,
      StartMS:         1776779040000,
      EndMS:           1776779280000,
      StepMS:          60000,
      RequiredStartMS: 1776779040000,
      RequiredEndMS:   1776779280000,
    })
    if err != nil { panic(err) }
    fmt.Printf("===== %s =====\n%s\n", fn, rendered.SQL)
  }
}
