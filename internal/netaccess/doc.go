// Package netaccess decides where the daemon listens and who may connect. In
// phase 1 that is loopback with a local token; in phase 2 it adds the
// tailnet through tsnet with Tailscale identity. It refuses to start on a
// public interface without authentication.
//
// It is a leaf.
package netaccess
