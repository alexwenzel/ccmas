// Command ccmas ist der Einstieg in die CCMAS-Kommandozentrale.
package main

import (
	"os"

	"github.com/alexwenzel/ccmas/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
