// Package api exposes the daemon over HTTP and WebSocket. It is the only
// contract between the daemon and every client (terminal, web, native app).
// It validates WebSocket origins and authenticates every request.
//
// It may depend on orchestrator, permissions, store and netaccess.
package api
