// Command aotus-desktop is the desktop window of Aotus. It is only a client of
// the daemon: closing the window never stops the employees. It needs cgo and
// the native WebView libraries, so it is built with the build tag desktop (see
// docs/QUICKSTART.md); without the tag this package is empty.
package main
