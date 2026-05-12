// Command thlibo is the middleware that AI clients invoke via hooks. It
// scans ~/.thlibo/processors/, routes tool output through the right
// processor, posts requests to thlibod, and returns the result. It has no
// knowledge of inference, model loading, or llamafile.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "thlibo: not yet implemented")
	os.Exit(1)
}
