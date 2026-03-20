package splithttp

import "testing"

func TestGenerateTokenishPaddingBase62SmallTargetAlwaysValid(t *testing.T) {
	config := &Config{}

	for target := 1; target <= 16; target++ {
		for i := 0; i < 64; i++ {
			padding := GenerateTokenishPaddingBase62(target)
			if padding == "" {
				t.Fatalf("expected padding for target %d", target)
			}
			if !config.IsPaddingValid(padding, int32(target), int32(target), PaddingMethodTokenish) {
				t.Fatalf("generated padding %q invalid for target %d", padding, target)
			}
		}
	}
}
