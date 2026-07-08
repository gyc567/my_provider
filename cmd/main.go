package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	"github.com/t-0-network/provider-sdk-go/network"
	"github.com/t-0-network/provider-sdk-go/provider"
	"my-provider/internal"
	"my-provider/internal/api"
	"my-provider/internal/handler"
)

type Config struct {
	NetworkPublicKey    provider.NetworkPublicKeyHexed
	ProviderPrivateKey  network.PrivateKeyHexed
	TZeroEndpoint       string
	ServerAddr          string
	APIKeys             []string // comma-separated in PROVIDER_API_KEYS env var
	PublishPayOutDefault bool
}

func main() {
	config := loadConfig()

	networkClient := initNetworkClient(config)

	shutdownFunc := startProviderServer(config, networkClient)
	defer shutdownFunc()

	// ✅ Step 1.1 is done. You successfully initialised starter template

	// TODO: Step 1.2 Share the generated public key from .env with t-0 team

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	publishCfg := internal.PublishConfig{PayOutDefault: config.PublishPayOutDefault}
	go internal.PublishQuotes(ctx, networkClient, publishCfg)

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

	return Config{
		NetworkPublicKey:     provider.NetworkPublicKeyHexed(os.Getenv("NETWORK_PUBLIC_KEY")),
		ProviderPrivateKey:   network.PrivateKeyHexed(os.Getenv("PROVIDER_PRIVATE_KEY")),
		TZeroEndpoint:        os.Getenv("TZERO_ENDPOINT"),
		ServerAddr:           ":" + os.Getenv("PORT"),
		APIKeys:              apiKeys,
		PublishPayOutDefault: publishPayOutDefault,
	}
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

func startProviderServer(config Config, networkClient paymentconnect.NetworkServiceClient) func() {
	sdkHandler, err := provider.NewHttpHandler(
		config.NetworkPublicKey,
		provider.Handler(paymentconnect.NewProviderServiceHandler,
			paymentconnect.ProviderServiceHandler(handler.NewProviderServiceImplementation(networkClient))),
	)
	if err != nil {
		log.Fatalf("Failed to create provider service handler: %v", err)
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

	// Root mux: SDK callback under /tzero.v1.payment.ProviderService/, our
	// product API under /api/v1/. Both run on the same port via the SDK's
	// StartServer (which accepts any http.Handler).
	rootMux := http.NewServeMux()
	rootMux.Handle("/tzero.v1.payment.ProviderService/", sdkHandler)
	rootMux.Handle("/api/v1/", apiHandler)

	shutdownFunc, err := provider.StartServer(rootMux, provider.WithAddr(config.ServerAddr))
	if err != nil {
		log.Fatalf("Failed to start provider service: %v", err)
	}

	log.Printf("✅ Step 1.1: Provider server initialized on %s (api=/api/v1, sdk=/tzero.v1.payment.ProviderService)\n", config.ServerAddr)

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