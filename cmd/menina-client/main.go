package main

import (
	"os"

	"github.com/moureau-dev/menina/internal/clientcli"
)

func main() {
	os.Exit(clientcli.Execute())
}
