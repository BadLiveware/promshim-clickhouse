package model

import (
	"fmt"
	"regexp"

	commonmodel "github.com/prometheus/common/model"
)

type LabelReplaceConfig struct {
	Dst   string
	Repl  string
	Src   string
	Regex *regexp.Regexp
}

type LabelJoinConfig struct {
	Dst       string
	Separator string
	SrcLabels []string
}

func BuildLabelReplaceConfig(dst, repl, src, regexStr string) (LabelReplaceConfig, error) {
	regex, err := regexp.Compile("^(?s:" + regexStr + ")$")
	if err != nil {
		return LabelReplaceConfig{}, fmt.Errorf("invalid regular expression in label_replace(): %s", regexStr)
	}
	if !commonmodel.LegacyValidation.IsValidLabelName(dst) {
		return LabelReplaceConfig{}, fmt.Errorf("invalid destination label name in label_replace(): %s", dst)
	}
	return LabelReplaceConfig{Dst: dst, Repl: repl, Src: src, Regex: regex}, nil
}

func BuildLabelJoinConfig(dst, sep string, srcLabels []string) (LabelJoinConfig, error) {
	for _, src := range srcLabels {
		if !commonmodel.LegacyValidation.IsValidLabelName(src) {
			return LabelJoinConfig{}, fmt.Errorf("invalid source label name in label_join(): %s", src)
		}
	}
	if !commonmodel.LegacyValidation.IsValidLabelName(dst) {
		return LabelJoinConfig{}, fmt.Errorf("invalid destination label name in label_join(): %s", dst)
	}
	return LabelJoinConfig{Dst: dst, Separator: sep, SrcLabels: append([]string(nil), srcLabels...)}, nil
}
