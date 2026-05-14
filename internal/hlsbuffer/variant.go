package hlsbuffer

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

func SelectVariant(variants []Variant, outputHeight, maxVariantHeight int) (Variant, error) {
	candidates := make([]Variant, 0, len(variants))
	for _, v := range variants {
		if maxVariantHeight > 0 && v.Height > maxVariantHeight {
			continue
		}
		if !variantHasVideoCodec(v) {
			continue
		}
		candidates = append(candidates, v)
	}
	if len(candidates) == 0 {
		return Variant{}, fmt.Errorf("hls playlist: no compatible video variants")
	}

	withHeight := candidatesWithHeight(candidates)
	if len(withHeight) > 0 {
		adequate := make([]Variant, 0, len(withHeight))
		for _, v := range withHeight {
			if v.Height >= outputHeight {
				adequate = append(adequate, v)
			}
		}
		if len(adequate) > 0 {
			sort.SliceStable(adequate, func(i, j int) bool {
				if adequate[i].Height != adequate[j].Height {
					return adequate[i].Height < adequate[j].Height
				}
				return bandwidthRank(adequate[i].Bandwidth) < bandwidthRank(adequate[j].Bandwidth)
			})
			return adequate[0], nil
		}

		sort.SliceStable(withHeight, func(i, j int) bool {
			if withHeight[i].Height != withHeight[j].Height {
				return withHeight[i].Height > withHeight[j].Height
			}
			return bandwidthRank(withHeight[i].Bandwidth) < bandwidthRank(withHeight[j].Bandwidth)
		})
		return withHeight[0], nil
	}

	withBandwidth := candidatesWithBandwidth(candidates)
	if len(withBandwidth) > 0 {
		sort.SliceStable(withBandwidth, func(i, j int) bool {
			return withBandwidth[i].Bandwidth < withBandwidth[j].Bandwidth
		})
		return withBandwidth[0], nil
	}

	return candidates[0], nil
}

func VariantCompatible(old, next Variant) bool {
	return old.URI == next.URI &&
		old.Width == next.Width &&
		old.Height == next.Height &&
		strings.EqualFold(strings.TrimSpace(old.Codecs), strings.TrimSpace(next.Codecs))
}

func candidatesWithHeight(variants []Variant) []Variant {
	out := make([]Variant, 0, len(variants))
	for _, v := range variants {
		if v.Height > 0 {
			out = append(out, v)
		}
	}
	return out
}

func candidatesWithBandwidth(variants []Variant) []Variant {
	out := make([]Variant, 0, len(variants))
	for _, v := range variants {
		if v.Bandwidth > 0 {
			out = append(out, v)
		}
	}
	return out
}

func bandwidthRank(bandwidth int) int {
	if bandwidth <= 0 {
		return math.MaxInt
	}
	return bandwidth
}

func variantHasVideoCodec(v Variant) bool {
	codecs := strings.TrimSpace(v.Codecs)
	if codecs == "" {
		return true
	}
	for _, codec := range strings.Split(codecs, ",") {
		c := strings.ToLower(strings.TrimSpace(codec))
		switch {
		case strings.HasPrefix(c, "avc1"),
			strings.HasPrefix(c, "avc3"),
			strings.HasPrefix(c, "hvc1"),
			strings.HasPrefix(c, "hev1"),
			strings.HasPrefix(c, "mp4v"):
			return true
		}
	}
	return false
}
