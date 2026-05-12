// Command thlibod is the thlibo inference daemon. It loads the Gemma 4 E4B
// model via llamafile once, stays warm, and serves inference requests over
// IPC. It has no knowledge of processors, routing, or AI clients.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "thlibod: not yet implemented")
	os.Exit(1)
}
