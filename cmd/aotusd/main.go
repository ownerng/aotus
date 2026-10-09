// Command aotusd is the daemon: a single static binary with no window.
package main

import (
	"flag"
	"fmt"

	"aotus/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("aotusd", version.Version)
		return
	}
	fmt.Println("aotusd", version.Version, "- not implemented yet, see docs/STATUS.md")
}
