package main

import "testing"

func TestWindowFitsTheScreen(t *testing.T) {
	for _, tc := range []struct {
		name         string
		areaW, areaH int
		w, h         int
	}{
		{"large screen keeps the wanted size", 2560, 1400, wantWidth, wantHeight},
		{"13-inch at 1280x800, top bar taken", 1280, 768, wantWidth, 706},
		{"13-inch at 150% scaling (1280x720 less the panel)", 1280, 690, wantWidth, 634},
		{"small 1024x600 netbook", 1024, 576, 942, 529},
		{"tiny work area never goes below the minimum", 600, 400, minWidth, minHeight},
		{"unknown work area keeps the wanted size", 0, 0, wantWidth, wantHeight},
	} {
		w, h := fitWindow(tc.areaW, tc.areaH)
		if w != tc.w || h != tc.h {
			t.Errorf("%s: fitWindow(%d, %d) = %dx%d, want %dx%d", tc.name, tc.areaW, tc.areaH, w, h, tc.w, tc.h)
		}
		if tc.areaW >= minWidth && tc.areaH >= minHeight && (w > tc.areaW || h > tc.areaH) {
			t.Errorf("%s: %dx%d does not fit in the work area %dx%d", tc.name, w, h, tc.areaW, tc.areaH)
		}
	}
}
