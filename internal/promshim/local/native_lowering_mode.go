package promshim

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

func parseNativeLoweringMode(raw string) (NativeLoweringMode, error) {
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

func normalizeNativeLoweringMode(mode NativeLoweringMode) NativeLoweringMode {
	normalized, err := parseNativeLoweringMode(string(mode))
	if err != nil {
		return NativeLoweringModePrefer
	}
	return normalized
}

func (mode NativeLoweringMode) enablesNativePlanning() bool {
	return normalizeNativeLoweringMode(mode) != NativeLoweringModeOff
}

func (mode NativeLoweringMode) forcesNativeRoot() bool {
	return normalizeNativeLoweringMode(mode) == NativeLoweringModeForceSupported
}

func (mode NativeLoweringMode) forcesExplainResponse() bool {
	return normalizeNativeLoweringMode(mode) == NativeLoweringModeExplain
}
