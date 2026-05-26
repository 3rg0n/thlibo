// Package builtins embeds the built-in processors into the thlibo
// binary. The middleware's registry scanner uses this as the Builtin
// source so thlibo works out of the box without any installer having
// run yet (spec §Built-in processors, gate row C4).
//
// User processors in ~/.thlibo/processors/ override any built-in with
// the same name (gate row C5).
//
// This file lives at the root of the processors/ directory (alongside
// the folders it embeds) so go:embed's path is trivially correct.
package builtins

import "embed"

// FS is the embedded tree of built-in processors. Top-level entries
// are processor folders; each folder contains a processor.yaml or
// processor.md descriptor and (for script processors) the entry
// script file.
//
//go:embed all:git-filter all:npm-filter all:cargo-filter all:compress all:casefolder all:shorthand all:stacktrace-filter all:pytest-filter all:ndjson-filter all:pdf-to-md all:cordon-filter all:lint-filter
var FS embed.FS
