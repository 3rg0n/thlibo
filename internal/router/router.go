// Package router builds the routing prompt from the processor registry and
// calls the daemon to decide which processor (or chain) should handle a
// given tool output. A fast-path regex check precedes the daemon call.
package router
