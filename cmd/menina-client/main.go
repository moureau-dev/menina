package main

import (
	"os"

	"github.com/moureau-dev/menina/internal/cli/client"
)

func main() {
	os.Exit(client.Execute())
}
