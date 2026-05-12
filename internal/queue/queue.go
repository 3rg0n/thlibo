// Package queue implements the daemon's concurrency control: 1 active
// generation, 10 queued, immediate queue-full error, and per-client
// cancellation on disconnect.
package queue
