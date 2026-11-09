package main

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"github.com/alexwenzel/ccmas/internal/fx"
	"github.com/alexwenzel/ccmas/internal/orakel"
)

// version wird beim Build per -ldflags "-X main.version=..." überschrieben.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "Version ausgeben und beenden")
	flag.Parse()
	if *showVersion {
		fmt.Println("orakel", version)
		return
	}

	r := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0))
	orakel.NewMainMenu(r).Run(os.Stdin, os.Stdout)
	fmt.Print(fx.Wave)
	fmt.Println(fx.Rainbow(os.Stdout, "Tschüss!", 0))
}
