package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aidmedium/stellar-anchor/internal/alert"
	"aidmedium/stellar-anchor/internal/audit"
	"aidmedium/stellar-anchor/internal/auth"
	"aidmedium/stellar-anchor/internal/chain"
	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/customer"
	"aidmedium/stellar-anchor/internal/db"
	"aidmedium/stellar-anchor/internal/interactive"
	"aidmedium/stellar-anchor/internal/kyc"
	"aidmedium/stellar-anchor/internal/payout"
	"aidmedium/stellar-anchor/internal/sap"
	"aidmedium/stellar-anchor/internal/secure"
	"aidmedium/stellar-anchor/internal/toml"
	"aidmedium/stellar-anchor/internal/withdrawal"

	"github.com/go-chi/chi"
	chimiddleware "github.com/go-chi/chi/middleware"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	_ = godotenv.Load(".env")
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mongoDB, err := db.Connect(ctx, cfg.DatabaseURL, cfg.DatabaseName)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to MongoDB")
	}
	box, err := secure.NewBox(cfg.EncryptionKey)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid encryption key")
	}
	customerStore, err := customer.NewMongoStore(ctx, mongoDB, box)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to prepare customer store")
	}
	withdrawalStore, err := withdrawal.NewMongoStore(ctx, mongoDB, box)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to prepare withdrawal store")
	}

	auditLog := audit.NewMongoLogger(mongoDB)
	alerts := alert.NewWebhook(cfg.AlertWebhookURL)
	platform := sap.NewClient(cfg.PlatformURL, cfg.PlatformAPISecret)
	core := payout.NewClient(cfg.CoreAPIURL, cfg.CoreAPISecret)
	horizon := chain.NewHorizon(cfg.HorizonURL)

	var verifier kyc.Verifier = kyc.Testnet{}
	if cfg.Network == config.Mainnet {
		verifier = kyc.NewDojah(cfg.DojahBaseURL, cfg.DojahAppID, cfg.DojahSecretKey)
	}
	customers := customer.NewService(customerStore, verifier, auditLog)
	withdrawals := withdrawal.NewService(withdrawalStore, platform, customers, core, auditLog,
		cfg.Network, "stellar:USDC:"+cfg.USDCIssuer, cfg.NGNPerUSDC, cfg.NGNPerXLM)
	processor := withdrawal.NewProcessor(withdrawals, horizon, alerts, cfg.DistributionAccount)
	reconciler := withdrawal.NewReconciler(processor, withdrawal.NewMongoCursorStore(mongoDB))

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/.well-known/stellar.toml", toml.Handler(cfg))
	r.Handle("/sep24/interactive", interactive.NewHandler(cfg.InteractiveJWTSecret, platform, customers, withdrawals, core))
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireCallbackAuth(cfg.CallbackAPISecret))
		customer.NewHandler(customers).Routes(r)
	})

	go processor.Run(ctx, cfg.PollInterval)
	go reconciler.Run(ctx, cfg.ReconcileInterval)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Info().
		Str("addr", srv.Addr).
		Str("network", cfg.Network).
		Str("home_domain", cfg.HomeDomain).
		Str("web_auth_domain", cfg.WebAuthDomain).
		Str("distribution_account", cfg.DistributionAccount).
		Msg("business server ready")

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal().Err(err).Msg("server error")
	}
}
