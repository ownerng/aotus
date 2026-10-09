//go:build !desktop

package main

import (
	"fmt"
	"os"
)

// Without the build tag there is no window: say how to build the real app.
func main() {
	fmt.Fprintln(os.Stderr, "aotus-desktop was built without the desktop tag; see docs/QUICKSTART.md (make desktop).")
	os.Exit(1)
}
