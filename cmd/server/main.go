package main

import (
	"shopnexus-remastered/internal/app"

	"go.uber.org/fx"
)

func main() {
	fx.New(app.Module).Run()
}
