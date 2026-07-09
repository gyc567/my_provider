package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	httpSwagger "github.com/swaggo/http-swagger"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	"github.com/t-0-network/provider-sdk-go/network"
	"github.com/t-0-network/provider-sdk-go/provider"
	_ "my-provider/docs"
	"my-provider/internal"
	"my-provider/internal/api"
	"my-provider/internal/handler"
	"my-provider/internal/quote"
)

type Config struct {
	NetworkPublicKey      provider.NetworkPublicKeyHexed
	ProviderPrivateKey    network.PrivateKeyHexed
	TZeroEndpoint         string
	ServerAddr            string
	DBPath                string
	APIKeys               []string
	PublishPayOutDefault  bool
}

// @title my-provider API
// @version 1.0
// @description Quote management REST API for the t-0 Network provider.
// @host api.agtpay.xyz
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
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

	publisher := quote.NewPublisher(store, networkClient, config.PublishPayOutDefault)

	apiHandler := api.NewHandler(store, publisher, networkClient, config.APIKeys)

	shutdownFunc := startProviderServer(config, networkClient, apiHandler)
	defer shutdownFunc()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go internal.PublishQuotes(ctx, publisher)
	go internal.GetQuote(ctx, networkClient)

	waitForShutdownSignal(cancel, shutdownFunc)
}

func loadConfig() Config {
	if err := godotenv.Load(".env"); err != nil {
		log.Fatalf("Failed to load .env file: %v", err)
	}

	return Config{
		NetworkPublicKey:     provider.NetworkPublicKeyHexed(os.Getenv("NETWORK_PUBLIC_KEY")),
		ProviderPrivateKey:   network.PrivateKeyHexed(os.Getenv("PROVIDER_PRIVATE_KEY")),
		TZeroEndpoint:        os.Getenv("TZERO_ENDPOINT"),
		ServerAddr:           ":" + os.Getenv("PORT"),
		DBPath:               getEnv("DB_PATH", "./data/quotes.db"),
		APIKeys:              splitKeys(os.Getenv("PROVIDER_API_KEYS")),
		PublishPayOutDefault: getEnvBool("PUBLISH_PAY_OUT_DEFAULT", true),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Fatalf("Invalid boolean value for %s: %s", key, v)
	}
	return b
}

func splitKeys(keys string) []string {
	if keys == "" {
		return nil
	}
	parts := strings.Split(keys, ",")
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

func startProviderServer(config Config, networkClient paymentconnect.NetworkServiceClient, apiHandler *api.Handler) func() {
	providerServiceHandler, err := provider.NewHttpHandler(
		config.NetworkPublicKey,
		provider.Handler(paymentconnect.NewProviderServiceHandler,
			paymentconnect.ProviderServiceHandler(handler.NewProviderServiceImplementation(networkClient))),
	)
	if err != nil {
		log.Fatalf("Failed to create provider service handler: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", providerServiceHandler)
	mux.Handle("/api/", apiHandler.Router())
	mux.Handle("/swagger/", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	shutdownFunc, err := provider.StartServer(
		mux,
		provider.WithAddr(config.ServerAddr),
	)
	if err != nil {
		log.Fatalf("Failed to start provider server: %v", err)
	}

	log.Printf("✅ Provider server initialized on %s\n", config.ServerAddr)

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
