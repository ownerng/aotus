// Package permissions decides whether an employee may perform an action
// (run a command, write outside its folder, reach a network destination),
// asks the user for approval when needed, and writes the audit log.
//
// It may depend on store only.
package permissions
