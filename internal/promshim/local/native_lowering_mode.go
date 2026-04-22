package local

import (
	"fmt"
	"strings"
)

type NativeLoweringMode string

const (
	NativeLoweringModeOff            NativeLoweringMode = "off"
	NativeLoweringModeExplain        NativeLoweringMode = "explain"
	NativeLoweringModeShadow         NativeLoweringMode = "shadow"
	NativeLoweringModePrefer         NativeLoweringMode = "prefer"
	NativeLoweringModeForceSupported NativeLoweringMode = "force_supported"
)

func ParseNativeLoweringMode(raw string) (NativeLoweringMode, error) {
	mode := NativeLoweringMode(strings.ToLower(strings.TrimSpace(raw)))
	switch mode {
	case "", NativeLoweringModePrefer:
		return NativeLoweringModePrefer, nil
	case NativeLoweringModeOff, NativeLoweringModeExplain, NativeLoweringModeShadow, NativeLoweringModeForceSupported:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported native lowering mode %q", raw)
	}
}

func NormalizeNativeLoweringMode(mode NativeLoweringMode) NativeLoweringMode {
	normalized, err := ParseNativeLoweringMode(string(mode))
	if err != nil {
		return NativeLoweringModePrefer
	}
	return normalized
}

func (mode NativeLoweringMode) EnablesNativePlanning() bool {
	return NormalizeNativeLoweringMode(mode) != NativeLoweringModeOff
}

func (mode NativeLoweringMode) ForcesNativeRoot() bool {
	return NormalizeNativeLoweringMode(mode) == NativeLoweringModeForceSupported
}

func (mode NativeLoweringMode) ForcesExplainResponse() bool {
	return NormalizeNativeLoweringMode(mode) == NativeLoweringModeExplain
}
