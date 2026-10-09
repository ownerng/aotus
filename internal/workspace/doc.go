// Package workspace gives every employee its own working folder inside the
// data directory, rejects any path that escapes it, builds the scrubbed
// environment (an explicit allow-list) a child process receives, and moves
// deleted folders to the trash.
//
// It is a leaf.
package workspace
