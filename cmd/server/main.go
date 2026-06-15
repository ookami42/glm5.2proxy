package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"glm5.2proxy/internal/app"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	service, err := app.New()
	if err != nil {
		log.Fatal(err)
	}
	if err := service.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
