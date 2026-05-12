// Package daemon owns the thlibod lifecycle: lock acquisition, llamafile
// child process management, readiness polling, graceful shutdown, and
// crash-restart with a lifetime attempt cap.
package daemon
