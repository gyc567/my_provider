package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	httpSwagger "github.com/swaggo/http-swagger"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	paymentintentproviderconnect "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider/providerconnect"
	paymentintentrecipientconnect "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient/recipientconnect"
	"github.com/t-0-network/provider-sdk-go/network"
	"github.com/t-0-network/provider-sdk-go/provider"
	_ "my-provider/docs"
	"my-provider/internal"
	"my-provider/internal/api"
	"my-provider/internal/handler"
	localpayment "my-provider/internal/payment"
	"my-provider/internal/paymentintent"
	paymentintentprovider "my-provider/internal/paymentintent/provider"
	paymentintentrecipient "my-provider/internal/paymentintent/recipient"
	"my-provider/internal/quote"
	"my-provider/internal/quoteapi"
	"my-provider/internal/settlement"
)

// Build-time variables. Injected via -ldflags in the deploy script and Dockerfile.
var (
	BuildVersion   = "dev"
	BuildCommit    = "unknown"
	BuildTime      = "unknown"
	BuildGoVersion = runtime.Version()
)

type Config struct {
	NetworkPublicKey     provider.NetworkPublicKeyHexed
	ProviderPrivateKey   network.PrivateKeyHexed
	TZeroEndpoint        string
	ServerAddr           string
	DBPath               string
	APIKeys                    []string // comma-separated in PROVIDER_API_KEYS env var
	PublishPayOutDefault       bool
	PublishPayInDefault        bool
	PaymentBaseURL             string
	SettlementWebhookURL       string
	SettlementWebhookSecret    string
	LastLookTolerancePercent   float64
}

// @title my-provider API
// @version 1.0
// @description Quote management REST API for the t-0 Network provider.
// @host api.agtpay.xyz
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

// versionResponse is returned by GET /version.
type versionResponse struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// healthResponse is returned by GET /health.
type healthResponse struct {
	Status  string `json:"status"`
	Commit  string `json:"commit"`
	Version string `json:"version"`
}

func versionHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versionResponse{
		Version:   BuildVersion,
		Commit:    BuildCommit,
		BuildTime: BuildTime,
		GoVersion: BuildGoVersion,
	})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{
		Status:  "ok",
		Commit:  BuildCommit,
		Version: BuildVersion,
	})
}

func main() {
	config := loadConfig()

	networkClient := initNetworkClient(config)

	store, err := quote.NewSQLiteStore(config.DBPath)
	if err != nil {
		log.Fatalf("Failed to create quote store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("Failed to close quote store: %v", err)
		}
	}()

	paymentStore, err := localpayment.NewSQLiteStore(config.DBPath)
	if err != nil {
		log.Fatalf("Failed to create payment store: %v", err)
	}
	defer func() {
		if err := paymentStore.Close(); err != nil {
			log.Printf("Failed to close payment store: %v", err)
		}
	}()

	settlementStore, err := settlement.NewSQLiteStore(config.DBPath)
	if err != nil {
		log.Fatalf("Failed to create settlement store: %v", err)
	}
	defer func() {
		if err := settlementStore.Close(); err != nil {
			log.Printf("Failed to close settlement store: %v", err)
		}
	}()

	paymentIntentStore, err := paymentintent.NewSQLiteStore(config.DBPath)
	if err != nil {
		log.Fatalf("Failed to create payment intent store: %v", err)
	}
	defer func() {
		if err := paymentIntentStore.Close(); err != nil {
			log.Printf("Failed to close payment intent store: %v", err)
		}
	}()

	publisher := quote.NewPublisher(store, networkClient, config.PublishPayOutDefault, config.PublishPayInDefault)

	shutdownFunc := startProviderServer(config, networkClient, store, paymentStore, settlementStore, paymentIntentStore, publisher)
	defer shutdownFunc()

	// ✅ Step 1.1 is done. You successfully initialised starter template

	// TODO: Step 1.2 Share the generated public key from .env with t-0 team

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go internal.PublishQuotes(ctx, publisher)

	// TODO: Step 1.4 Verify that quotes for target currency are successfully received
	go internal.GetQuote(ctx, networkClient)

	waitForShutdownSignal(cancel, shutdownFunc)

	// TODO: Step 2.2 Deploy your integration and provide t-0 team with the base URL
	// TODO: Step 2.3 Test payment submission
	// TODO: Step 2.5 Ask t-0 team to submit a payment to test your payOut endpoint
}

func loadConfig() Config {
	if err := godotenv.Load(".env"); err != nil {
		log.Fatalf("Failed to load .env file: %v", err)
	}

	apiKeys := parseAPIKeys(os.Getenv("PROVIDER_API_KEYS"))
	if len(apiKeys) == 0 {
		log.Println("WARN: PROVIDER_API_KEYS is empty — /api/v1/quotes/pay-out will reject all requests with 401")
	}

	publishPayOutDefault := true // default for backward compatibility
	if v := os.Getenv("PUBLISH_PAY_OUT_DEFAULT"); v != "" {
		switch strings.ToLower(v) {
		case "false", "0", "no":
			publishPayOutDefault = false
		case "true", "1", "yes":
			publishPayOutDefault = true
		default:
			log.Printf("WARN: PUBLISH_PAY_OUT_DEFAULT=%q is not a recognized boolean, defaulting to true", v)
		}
	}

	publishPayInDefault := true // publish sample pay-in quotes by default
	if v := os.Getenv("PUBLISH_PAY_IN_DEFAULT"); v != "" {
		switch strings.ToLower(v) {
		case "false", "0", "no":
			publishPayInDefault = false
		case "true", "1", "yes":
			publishPayInDefault = true
		default:
			log.Printf("WARN: PUBLISH_PAY_IN_DEFAULT=%q is not a recognized boolean, defaulting to true", v)
		}
	}

	paymentBaseURL := os.Getenv("PAYMENT_BASE_URL")
	if paymentBaseURL == "" {
		paymentBaseURL = "https://example.com/pay"
		log.Printf("WARN: PAYMENT_BASE_URL is empty — defaulting to %s", paymentBaseURL)
	}

	lastLookTolerance := 1.0
	if v := os.Getenv("LAST_LOOK_TOLERANCE_PERCENT"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			lastLookTolerance = parsed
		} else {
			log.Printf("WARN: LAST_LOOK_TOLERANCE_PERCENT=%q is not a valid number, defaulting to 1.0", v)
		}
	}

	return Config{
		NetworkPublicKey:         provider.NetworkPublicKeyHexed(os.Getenv("NETWORK_PUBLIC_KEY")),
		ProviderPrivateKey:       network.PrivateKeyHexed(os.Getenv("PROVIDER_PRIVATE_KEY")),
		TZeroEndpoint:            os.Getenv("TZERO_ENDPOINT"),
		ServerAddr:               ":" + os.Getenv("PORT"),
		DBPath:                   getEnv("DB_PATH", "./data/quotes.db"),
		APIKeys:                  apiKeys,
		PublishPayOutDefault:     publishPayOutDefault,
		PublishPayInDefault:      publishPayInDefault,
		PaymentBaseURL:           paymentBaseURL,
		SettlementWebhookURL:     os.Getenv("SETTLEMENT_WEBHOOK_URL"),
		SettlementWebhookSecret:  os.Getenv("SETTLEMENT_WEBHOOK_SECRET"),
		LastLookTolerancePercent: lastLookTolerance,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseAPIKeys(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func initNetworkClient(config Config) paymentconnect.NetworkServiceClient {
	networkClient, err := network.NewServiceClient(
		config.ProviderPrivateKey,
		paymentconnect.NewNetworkServiceClient,
		network.WithBaseURL(config.TZeroEndpoint),
	)
	if err != nil {
		log.Fatalf("Failed to create network service client: %v", err)
	}
	return networkClient
}

func startProviderServer(config Config, networkClient paymentconnect.NetworkServiceClient, store quote.Store, paymentStore localpayment.Store, settlementStore settlement.Store, paymentIntentStore paymentintent.Store, publisher *quote.Publisher) func() {
	var settlementNotifier settlement.Notifier = settlement.NewNoOpNotifier()
	if config.SettlementWebhookURL != "" {
		settlementNotifier = settlement.NewWebhookNotifier(config.SettlementWebhookURL, config.SettlementWebhookSecret)
	}

	sdkHandler, err := provider.NewHttpHandler(
		config.NetworkPublicKey,
		provider.Handler(paymentconnect.NewProviderServiceHandler,
			paymentconnect.ProviderServiceHandler(handler.NewProviderServiceImplementation(networkClient, paymentStore, settlementStore, settlementNotifier, config.LastLookTolerancePercent))),
	)
	if err != nil {
		log.Fatalf("Failed to create provider service handler: %v", err)
	}

	// Payment intent provider client and handler (Phase 3A).
	piProviderClient, err := network.NewServiceClient(
		config.ProviderPrivateKey,
		paymentintentproviderconnect.NewNetworkServiceClient,
		network.WithBaseURL(config.TZeroEndpoint),
	)
	if err != nil {
		log.Fatalf("Failed to create payment intent provider network client: %v", err)
	}
	piProviderHandler := paymentintentprovider.NewHandler(paymentIntentStore, paymentintentprovider.NewNetworkClient(piProviderClient), config.PaymentBaseURL)
	piSdkHandler, err := provider.NewHttpHandler(
		config.NetworkPublicKey,
		provider.Handler(paymentintentproviderconnect.NewProviderServiceHandler,
			paymentintentproviderconnect.ProviderServiceHandler(piProviderHandler)),
	)
	if err != nil {
		log.Fatalf("Failed to create payment intent provider SDK handler: %v", err)
	}

	// Payment intent recipient client and handler (Phase 3B).
	piRecipientClient, err := network.NewServiceClient(
		config.ProviderPrivateKey,
		paymentintentrecipientconnect.NewNetworkServiceClient,
		network.WithBaseURL(config.TZeroEndpoint),
	)
	if err != nil {
		log.Fatalf("Failed to create payment intent recipient network client: %v", err)
	}
	piRecipientHandler := paymentintentrecipient.NewHandler(paymentIntentStore)
	piRecipientSdkHandler, err := provider.NewHttpHandler(
		config.NetworkPublicKey,
		provider.Handler(paymentintentrecipientconnect.NewRecipientServiceHandler,
			paymentintentrecipientconnect.RecipientServiceHandler(piRecipientHandler)),
	)
	if err != nil {
		log.Fatalf("Failed to create payment intent recipient SDK handler: %v", err)
	}

	// Product-layer HTTP API (UpdateQuote push endpoint).
	apiHandler := api.NewRouter(api.Deps{
		NetworkClient:   networkClient,
		APIKeys:         config.APIKeys,
		MaxBodyBytes:    64 << 10,
		RequestsPerSec:  20,
		Burst:           40,
		UpstreamTimeout: 5 * 1e9, // 5s
		IdempotencyTTL:  60 * 1e9,
	})

	// quoteapiHandler exposes the snapshot/publish/network-quote endpoints.
	quoteapiHandler := quoteapi.NewHandler(store, publisher, networkClient, config.APIKeys)

	// paymentHandler exposes the payment lifecycle endpoints.
	paymentClient := localpayment.NewNetworkClient(networkClient)
	paymentHandler := localpayment.NewHandler(paymentStore, paymentClient, config.APIKeys)

	// settlementHandler exposes credit/ledger query endpoints.
	settlementHandler := settlement.NewAPIHandler(settlementStore, config.APIKeys)

	// paymentIntentProviderHandler exposes 3A admin endpoints under /api/v1/payment-intents/provider.
	paymentIntentProviderAPI := paymentintentprovider.NewAPIHandler(paymentIntentStore, paymentintentprovider.NewNetworkClient(piProviderClient), config.APIKeys)

	// paymentIntentRecipientHandler exposes 3B admin endpoints under /api/v1.
	paymentIntentRecipientAPI := paymentintentrecipient.NewAPIHandler(paymentIntentStore, paymentintentrecipient.NewNetworkClient(piRecipientClient), config.APIKeys)

	// Root mux: SDK callbacks, REST APIs, and Swagger docs.
	rootMux := http.NewServeMux()
	rootMux.Handle("/tzero.v1.payment.ProviderService/", sdkHandler)
	rootMux.Handle("/tzero.v1.payment_intent.provider.ProviderService/", piSdkHandler)
	rootMux.Handle("/tzero.v1.payment_intent.recipient.RecipientService/", piRecipientSdkHandler)
	rootMux.Handle("/api/v1/quotes/pay-out", apiHandler)
	rootMux.Handle("/api/v1/quotes", quoteapiHandler.Router())
	rootMux.Handle("/api/v1/quotes/", quoteapiHandler.Router())
	rootMux.Handle("/api/v1/payments", paymentHandler.Router())
	rootMux.Handle("/api/v1/payments/", paymentHandler.Router())
	rootMux.Handle("/api/v1/settlement", settlementHandler.Router())
	rootMux.Handle("/api/v1/settlement/", settlementHandler.Router())
	rootMux.Handle("/api/v1/payment-intents/provider/", http.StripPrefix("/api/v1/payment-intents/provider", paymentIntentProviderAPI.Router()))
	rootMux.Handle("/api/v1/", http.StripPrefix("/api/v1", paymentIntentRecipientAPI.Router()))
	rootMux.HandleFunc("GET /version", versionHandler)
	rootMux.HandleFunc("GET /health", healthHandler)
	rootMux.Handle("/swagger/", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	shutdownFunc, err := provider.StartServer(rootMux, provider.WithAddr(config.ServerAddr))
	if err != nil {
		log.Fatalf("Failed to start provider service: %v", err)
	}

	log.Printf("✅ Provider server initialized on %s (pay-out=/api/v1/quotes/pay-out, quotes=/api/v1/quotes/*, sdk=/tzero.v1.payment.ProviderService, swagger=/swagger, version=/version, health=/health)\n", config.ServerAddr)

	return func() {
		if err := shutdownFunc(context.Background()); err != nil {
			log.Fatalf("Failed to shutdown provider service: %v", err)
		}
	}
}

func waitForShutdownSignal(cancel context.CancelFunc, shutdownFunc func()) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()

	log.Println("Shutting down...")
	cancel()
	shutdownFunc()
}