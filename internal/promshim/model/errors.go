package model

import "errors"

var ErrDuplicateLabelsetTimestamps = errors.New("vector cannot contain metrics with the same labelset")
var ErrNonIncreasingChunkMerge = errors.New("chunked range merge encountered non-increasing timestamps")
