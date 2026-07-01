package main

import (
	"net/http"
	"os"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/db"
	"aidmedium/stellar-anchor/internal/middleware"
	"aidmedium/stellar-anchor/internal/sep10"
	"aidmedium/stellar-anchor/internal/sep12"
	"aidmedium/stellar-anchor/internal/sep24"
	"aidmedium/stellar-anchor/internal/sep31"
	"aidmedium/stellar-anchor/internal/sep6"
	"aidmedium/stellar-anchor/internal/toml"

	"github.com/go-chi/chi"
	chimiddleware "github.com/go-chi/chi/middleware"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	stellarkeypair "github.com/stellar/go-stellar-sdk/keypair"
)

func main() {
	_ = godotenv.Load(".env")

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Derive public keys from seeds at startup — fail fast if seeds are invalid
	anchorKP, err := stellarkeypair.ParseFull(cfg.AnchorSigningSeed)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid ANCHOR_SIGNING_SEED")
	}
	cfg.AnchorSigningKey = anchorKP.Address()

	distKP, err := stellarkeypair.ParseFull(cfg.DistributionSeed)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid DISTRIBUTION_SEED")
	}
	cfg.DistributionKey = distKP.Address()

	log.Info().
		Str("env", cfg.Env).
		Str("signing_key", cfg.AnchorSigningKey).
		Str("distribution_key", cfg.DistributionKey).
		Msg("stellar-anchor starting")

	mongoDB, err := db.Connect(cfg.DatabaseURL, "stellar_anchor")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to MongoDB")
	}
	log.Info().Msg("connected to MongoDB")

	// Wire up handlers with their MongoDB stores
	sep12h := sep12.NewHandler(sep12.NewMongoStore(mongoDB))
	sep24h := sep24.NewHandler(sep24.NewMongoStore(mongoDB), cfg)
	sep6h := sep6.NewHandler(sep6.NewMongoStore(mongoDB), cfg)
	sep31h := sep31.NewHandler(sep31.NewMongoStore(mongoDB), cfg)
	jwtMw := middleware.RequireSEP10JWT(cfg)

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(corsMiddleware)

	// Stellar anchor discovery — Stellar wallets fetch this first
	r.Get("/.well-known/stellar.toml", toml.Handler(cfg))

	// SEP-10: Wallet Authentication
	r.Get("/sep10/auth", sep10.GetChallenge(cfg))
	r.Post("/sep10/auth", sep10.PostChallenge(cfg))

	// SEP-12: KYC (all protected by SEP-10 JWT)
	r.Group(func(r chi.Router) {
		r.Use(jwtMw)
		r.Get("/sep12/customer", sep12h.GetCustomer)
		r.Put("/sep12/customer", sep12h.PutCustomer)
		r.Delete("/sep12/customer", sep12h.DeleteCustomer)
	})

	// SEP-24: Interactive deposit/withdrawal
	r.Get("/sep24/info", sep24h.Info)
	r.Group(func(r chi.Router) {
		r.Use(jwtMw)
		r.Post("/sep24/transactions/deposit/interactive", sep24h.InitiateDeposit)
		r.Post("/sep24/transactions/withdraw/interactive", sep24h.InitiateWithdraw)
		r.Get("/sep24/transaction", sep24h.GetTransaction)
	})

	// SEP-6: Programmatic deposit/withdrawal
	r.Get("/sep6/info", sep6h.Info)
	r.Group(func(r chi.Router) {
		r.Use(jwtMw)
		r.Get("/sep6/deposit", sep6h.Deposit)
		r.Get("/sep6/withdraw", sep6h.Withdraw)
		r.Get("/sep6/transaction", sep6h.GetTransaction)
	})

	// SEP-31: Cross-border B2B payments
	r.Get("/sep31/info", sep31h.Info)
	r.Group(func(r chi.Router) {
		r.Use(jwtMw)
		r.Post("/sep31/transactions", sep31h.CreateTransaction)
		r.Get("/sep31/transactions/{id}", sep31h.GetTransaction)
	})

	addr := ":" + cfg.Port
	log.Info().Str("addr", addr).Msg("stellar-anchor listening")
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
