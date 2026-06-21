package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/url"
	"os/exec"
	"runtime"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"glm5.2proxy/internal/app"
)

//go:embed cmd/desktop/frontend/dist
var assets embed.FS

type Desktop struct {
	service *app.Service
	cancel  context.CancelFunc
	done    chan struct{}
}

func (d *Desktop) startup(wailsCtx context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	go func() {
		defer close(d.done)
		if err := d.service.Run(ctx); err != nil {
			log.Printf("backend stopped: %v", err)
			wailsruntime.Quit(wailsCtx)
		}
	}()
}

func (d *Desktop) shutdown(_ context.Context) {
	if d.cancel != nil {
		d.cancel()
	}
	select {
	case <-d.done:
	case <-time.After(12 * time.Second):
	}
}

func (d *Desktop) Port() int {
	return d.service.Port()
}

func (d *Desktop) OpenExternalURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errInvalidURL()
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return errInvalidURL()
	}
	return openExternalURL(rawURL)
}

func main() {
	service, err := app.New()
	if err != nil {
		log.Fatal(err)
	}
	frontend, err := fs.Sub(assets, "cmd/desktop/frontend/dist")
	if err != nil {
		log.Fatal(err)
	}
	desktop := &Desktop{service: service, done: make(chan struct{})}
	err = wails.Run(&options.App{
		Title:  "glm5.2proxy",
		Width:  1180,
		Height: 760,
		AssetServer: &assetserver.Options{
			Assets: frontend,
		},
		OnStartup:  desktop.startup,
		OnShutdown: desktop.shutdown,
		Bind:       []any{desktop},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func errInvalidURL() error {
	return errors.New("URL de autenticacao invalida")
}

func openExternalURL(rawURL string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
	case "darwin":
		return exec.Command("open", rawURL).Start()
	default:
		return exec.Command("xdg-open", rawURL).Start()
	}
}
