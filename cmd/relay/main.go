// Commande relay : gestionnaire de connexions à distance (RDP, SSH, VNC, web)
// accessible depuis le navigateur, derrière un SSO.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/overnite22/relay-public/internal/api"
	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/store"
	"github.com/overnite22/relay-public/internal/vault"
	"github.com/overnite22/relay-public/web"
)

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func list(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	// "relay healthcheck" : sonde pour le HEALTHCHECK Docker (image sans shell).
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		addr := env("RELAY_LISTEN", ":8080")
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
		c := http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get("http://" + addr + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("arrêt sur erreur", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	dataDir := env("RELAY_DATA_DIR", "./data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	keyPath := env("RELAY_MASTER_KEY_FILE", filepath.Join(dataDir, "master.key"))
	key, created, err := vault.LoadOrCreateKey(keyPath)
	if err != nil {
		return err
	}
	if created {
		log.Warn("clé maître générée : sauvegardez-la, sans elle le coffre est illisible", "fichier", keyPath)
	}
	v, err := vault.New(key)
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(dataDir, "relay.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	var proxies []netip.Prefix
	for _, p := range list(env("RELAY_TRUSTED_PROXIES", "")) {
		pr, err := netip.ParsePrefix(p)
		if err != nil {
			return errors.New("RELAY_TRUSTED_PROXIES invalide : " + p)
		}
		proxies = append(proxies, pr)
	}
	mode := env("RELAY_AUTH_MODE", "userinfo")
	a, err := auth.New(auth.Config{
		Mode:           mode,
		UserinfoURL:    env("RELAY_OAUTH2_USERINFO_URL", "http://oauth2-proxy:4180/oauth2/userinfo"),
		Header:         env("RELAY_USER_HEADER", "X-Forwarded-Email"),
		TrustedProxies: proxies,
		DevUser:        env("RELAY_DEV_USER", ""),
		Allowed:        list(env("RELAY_ALLOWED_USERS", "")),
	})
	if err != nil {
		return err
	}
	if mode == "dev" {
		log.Warn("MODE DEV : authentification désactivée, ne jamais exposer cette instance")
	}

	srv := api.New(api.Options{
		Store:         st,
		DataDir:       dataDir,
		AdminUsers:    list(env("RELAY_ADMIN_USERS", "")),
		RecordingsDir: filepath.Join(dataDir, "recordings"),
		Vault:         v,
		Auth:          a,
		GuacdAddr:     env("RELAY_GUACD_ADDR", "guacd:4822"),
		BridgeHost:    env("RELAY_BRIDGE_HOST", ""),
		PublicOrigin:  strings.TrimRight(env("RELAY_PUBLIC_ORIGIN", ""), "/"),
		Static:        web.Handler(),
		Log:           log,
	})
	httpSrv := &http.Server{
		Addr:              env("RELAY_LISTEN", ":8080"),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.Scheduler(ctx)
	go srv.MonitorConnections(ctx)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}()
	log.Info("Relay démarré", "version", api.Version, "écoute", httpSrv.Addr, "auth", mode)
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
