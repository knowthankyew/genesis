package main

import (
	"fmt"
	"os"

	"github.com/knowthankyew/genesis/internal/cli"
)

func isBindingAllInterfaces(addr string) bool {
	return cli.IsBindingAllInterfaces(addr)
}

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
