package main

// Window size rules, kept free of the GUI so they are tested on every build.
const (
	wantWidth, wantHeight = 1100, 720 // the size the window asks for
	minWidth, minHeight   = 640, 440  // the smallest it may be made
	screenShare           = 0.92      // the most of a screen's work area it takes at first
)

// fitWindow returns the size the window opens at on a screen whose work area
// (the screen without its panels and docks) is areaW x areaH: the wanted size,
// shrunk to fit inside the work area with a margin, never below the minimum.
// An unknown work area (zero) keeps the wanted size.
func fitWindow(areaW, areaH int) (int, int) {
	w, h := wantWidth, wantHeight
	if areaW > 0 {
		w = min(w, int(float64(areaW)*screenShare))
	}
	if areaH > 0 {
		h = min(h, int(float64(areaH)*screenShare))
	}
	return max(w, minWidth), max(h, minHeight)
}
