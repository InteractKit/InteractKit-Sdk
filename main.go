package main

import (
	"interactkit/core"
)

func main() {
}

func runPipeline(
	config []byte,
) {
	runner := core.NewRunner(config)
	runner.Execute()
}
