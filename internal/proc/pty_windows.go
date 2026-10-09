//go:build windows

package proc

// openPTY is implemented with ConPTY in task P1-021. Until then Windows
// reports that terminals are not available, and callers fall back to the
// structured mode or tell the user.
func openPTY(PTYSpec) (ptyBackend, error) { return nil, ErrPTYUnsupported }
