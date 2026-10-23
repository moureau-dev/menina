package main

import (
	"os"

	"github.com/moureau-dev/menina/internal/cli/host"
)

func main() {
	os.Exit(host.Execute())
}
