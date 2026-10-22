package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Config struct {
	Listen string      `json:"listen"`
	DB     string      `json:"db"`
	Auth   AuthConfig  `json:"auth"`
	Image  ImageConfig `json:"image"`
}

func defaultConfig() Config {
	return Config{
		Listen: ":1337",
		DB:     "tabula.db",
		Image:  defaultImageConfig(),
	}
}

// loadConfig reads path over the defaults. A missing file means all defaults.
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if (cfg.Auth.User == "") != (cfg.Auth.Password == "") {
		return cfg, fmt.Errorf("%s: auth needs both user and password, or neither", path)
	}
	return cfg, nil
}

func main() {
	configPath := flag.String("config", "config.json", "path to the config file")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	store, err := OpenStore(cfg.DB)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer store.Close()
	icons, err := LoadIcons()
	if err != nil {
		log.Fatalf("icons: %v", err)
	}
	renderer, err := NewRenderer(cfg.Image)
	if err != nil {
		log.Fatalf("image config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	a := &api{store: store, icons: icons, renderer: renderer}
	notifier := NewNotifier(store, a.deviceFrame)
	a.changed = notifier.Changed
	go notifier.Run(ctx)
	a.routes(mux)
	webRoutes(mux)

	srv := &http.Server{Addr: cfg.Listen, Handler: basicAuth(cfg.Auth, mux), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	if cfg.Auth.User != "" {
		log.Printf("basic auth on for user %q", cfg.Auth.User)
	}
	log.Printf("listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http: %v", err)
	}
}
