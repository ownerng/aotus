// Package store is the persistence layer: SQLite in WAL mode (pure-Go driver,
// no cgo), schema migrations, and the file layout of the data directory.
//
// It is a leaf: it must not import any other internal package.
package store
