package main

import (
	_ "embed"
	"fmt"
)

// guide is how Pier works, written for people and coding agents alike, so
// an agent with the CLI can read it without the network.
//
//go:embed guide.md
var guide string

func docsCmd(args []string) error {
	fmt.Print(guide)
	return nil
}
