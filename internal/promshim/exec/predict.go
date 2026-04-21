package exec

import (
	"math"

	"ch-observability/internal/promshim/model"
	"github.com/prometheus/prometheus/util/kahansum"
)

func ApplyPredictLinear(duration float64, input model.RuntimeValue, params EvalParams) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("predict_linear requires matrix input, got %T", input)
	}
	evalTS := float64(params.EvaluationTime.UnixMilli())
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}
		slope, intercept := linearRegression(series.Values, evalTS)
		out = append(out, model.InstantSample{
			Metric:    model.DropMetricName(series.Metric),
			Timestamp: evalTS,
			Value:     slope*duration + intercept,
		})
	}
	return sortVectorSamples(out), nil
}

func linearRegression(samples []model.RangePoint, interceptTime float64) (slope, intercept float64) {
	var (
		n          float64
		sumX, cX   float64
		sumY, cY   float64
		sumXY, cXY float64
		sumX2, cX2 float64
		initY      float64
		constY     bool
	)
	initY = samples[0].Value
	constY = true
	for i, sample := range samples {
		if constY && i > 0 && sample.Value != initY {
			constY = false
		}
		n += 1.0
		x := (sample.Timestamp - interceptTime) / 1e3
		sumX, cX = kahansum.Inc(x, sumX, cX)
		sumY, cY = kahansum.Inc(sample.Value, sumY, cY)
		sumXY, cXY = kahansum.Inc(x*sample.Value, sumXY, cXY)
		sumX2, cX2 = kahansum.Inc(x*x, sumX2, cX2)
	}
	if constY {
		if math.IsInf(initY, 0) {
			return math.NaN(), math.NaN()
		}
		return 0, initY
	}
	sumX += cX
	sumY += cY
	sumXY += cXY
	sumX2 += cX2

	covXY := sumXY - sumX*sumY/n
	varX := sumX2 - sumX*sumX/n

	slope = covXY / varX
	intercept = sumY/n - slope*sumX/n
	return slope, intercept
}
