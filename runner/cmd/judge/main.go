// judge is the single binary baked into every language image.
//
//	judge serve   HTTP server run by the container (outside the sandbox)
//	judge box     supervisor run inside each sandbox
package main

import (
	"fmt"
	"os"

	"serverless-judge/runner/internal/box"
	"serverless-judge/runner/internal/serve"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: judge serve|box")
		os.Exit(64)
	}
	switch os.Args[1] {
	case "serve":
		serve.Main()
	case "box":
		box.Main()
	default:
		fmt.Fprintln(os.Stderr, "usage: judge serve|box")
		os.Exit(64)
	}
}
