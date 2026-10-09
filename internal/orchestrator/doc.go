// Package orchestrator runs employees: it creates, pauses and deletes them,
// keeps one supervised session per employee alive in the background, starts
// turns in parallel, streams events, cancels work, applies the restart policy
// and resumes sessions after a daemon restart.
//
// It may depend on provider, proc, workspace, permissions and store.
package orchestrator
