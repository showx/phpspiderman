package main

import (
	"os"

	"gospider/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
