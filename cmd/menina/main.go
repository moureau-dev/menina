package main

import (
	"os"

	"github.com/moureau-dev/menina/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
