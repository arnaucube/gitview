package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/alexflint/go-arg"
	"gitview/internal/config"
	"gitview/internal/routes"
)

var (
	version   = "dev"
	buildDate = "unknown"
)

type args struct {
	Config string `arg:"--config" default:"./config.yaml" help:"path to config file"`
}

func (args) Version() string {
	return fmt.Sprintf("gitview %s (built %s)", version, buildDate)
}

func main() {
	var a args
	arg.MustParse(&a)

	c, err := config.Read(a.Config)
	if err != nil {
		log.Fatal(err)
	}

	if err := UnveilPaths([]string{
		c.Dirs.Static,
		c.Repo.ScanPath,
		c.Dirs.Templates,
	},
		"r"); err != nil {
		log.Fatalf("unveil: %s", err)
	}

	mux := routes.Handlers(c)
	addr := fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
	log.Println("starting server on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
