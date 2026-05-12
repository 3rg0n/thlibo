// Command llamafile-stub mimics llamafile's observable behaviour for
// daemon integration tests. It speaks the daemon's SubprocessEngine wire
// protocol (see internal/daemon/engine.go):
//
//	stdin:  one JSON request per line
//	stdout: token lines, then a line "<<END>>" to end the response
//	stderr: a "READY" line after an optional load delay, then diagnostics
//
// Flags:
//
//	-load-delay duration   wait before emitting READY (default 0)
//	-token-delay duration  wait between tokens (default 0)
//	-tokens string         comma-separated tokens to emit per request
//	                       (default "Hello,-world")
//	-crash-after int       after N completed requests, exit with code 2
//	                       (simulates llamafile crash; default 0 = never)
//
// This binary is built only for tests; it is not shipped in release
// artefacts.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

type request struct {
	System      string  `json:"system"`
	User        string  `json:"user"`
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	TopK        int     `json:"top_k"`
	MaxTokens   int     `json:"max_tokens"`
}

func main() {
	loadDelay := flag.Duration("load-delay", 0, "delay before READY")
	tokenDelay := flag.Duration("token-delay", 0, "delay between tokens")
	tokensFlag := flag.String("tokens", "Hello,-world", "comma-separated tokens per request")
	crashAfter := flag.Int("crash-after", 0, "exit 2 after N completed requests (0 = never)")
	flag.Parse()

	if *loadDelay > 0 {
		time.Sleep(*loadDelay)
	}
	fmt.Fprintln(os.Stderr, "READY")

	tokens := strings.Split(*tokensFlag, ",")
	in := bufio.NewReader(os.Stdin)
	completed := 0
	for {
		line, err := in.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			return
		}
		var req request
		if jerr := json.Unmarshal(line, &req); jerr != nil {
			fmt.Fprintf(os.Stderr, "stub: bad request: %v\n", jerr)
			fmt.Println("<<END>>")
			continue
		}
		for _, tok := range tokens {
			if *tokenDelay > 0 {
				time.Sleep(*tokenDelay)
			}
			fmt.Println(tok)
		}
		fmt.Println("<<END>>")
		completed++
		if *crashAfter > 0 && completed >= *crashAfter {
			os.Exit(2)
		}
		if err != nil {
			return
		}
	}
}
