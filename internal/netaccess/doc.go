// Package netaccess decides where the daemon listens and who may connect.
// There are two kinds of listener and nothing else: loopback, whose callers
// carry the local token, and the tailnet, whose callers are identified by the
// Tailscale running on this machine (ADR 0014). It cannot listen on a
// wildcard or a public address, whatever the configuration says.
//
// It is a leaf.
package netaccess
