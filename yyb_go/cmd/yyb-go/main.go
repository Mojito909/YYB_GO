package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"yyb_go/internal/httpapi"
)

func envBool(key string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(key)))
	return err == nil && value
}

func main() {
	host := flag.String("host", "127.0.0.1", "listen host")
	port := flag.Int("port", 8000, "listen port")
	resourceRoot := flag.String("resource-root", filepath.Join(".", "resource"), "page resource directory (templates/static)")
	dataRoot := flag.String("data-root", "", "runtime data directory (db/avatars/qr), defaults to -resource-root")
	dbFilename := flag.String("db", httpapi.DefaultDBFilename, "SQLite database filename under data-root/db")
	tcpProxy := flag.String("tcp-proxy", "", "optional TCP proxy: socks5://host:port or http-connect://host:port")
	barkServer := flag.String("bark-server", strings.TrimSpace(os.Getenv("YYB_BARK_SERVER")), "Bark push server, defaults to https://api.day.app")
	trustProxy := flag.Bool("trust-proxy", envBool("YYB_TRUST_PROXY"), "trust X-Forwarded-For / X-Real-IP from a reverse proxy")
	secureCookie := flag.Bool("secure-cookie", envBool("YYB_SECURE_COOKIE"), "always mark the admin session cookie as Secure")
	flag.Parse()

	cfg := httpapi.Config{
		ResourceRoot:   *resourceRoot,
		DataRoot:       *dataRoot,
		DBFilename:     *dbFilename,
		TCPProxy:       *tcpProxy,
		BarkServer:     *barkServer,
		TrustProxy:     *trustProxy,
		SecureCookie:   *secureCookie,
		SessionTTL:     30 * time.Minute,
		RequestTimeout: 8 * time.Second,
		AvatarTimeout:  10 * time.Second,
		QRSessionTTL:   5 * time.Minute,
		AuthTTL:        24 * time.Hour,
	}

	app, err := httpapi.NewApp(cfg)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}
	defer app.Close()

	addr := fmt.Sprintf("%s:%d", *host, *port)
	// WriteTimeout 需覆盖二维码创建的最长链路（约 43s）。
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("YYB Go service listening on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
