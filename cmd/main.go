package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	"github.com/t-0-network/provider-sdk-go/network"
	"github.com/t-0-network/provider-sdk-go/provider"
	"my-provider/internal"
	"my-provider/internal/handler"
)

type Config struct {
	NetworkPublicKey   provider.NetworkPublicKeyHexed
	ProviderPrivateKey network.PrivateKeyHexed
	TZeroEndpoint      string
	ServerAddr         string
}

func main() {
	config := loadConfig()

	networkClient := initNetworkClient(config)

	shutdownFunc := startProviderServer(config, networkClient)
	defer shutdownFunc()

	// ✅ Step 1.1 is done. You successfully initialised starter template

	// TODO: Step 1.2 Share the generated public key from .env with t-0 team

	// TODO: Step 1.3 Replace publishQuotes with your own quote publishing logic

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go internal.PublishQuotes(ctx, networkClient)

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

	return Config{
		NetworkPublicKey:   provider.NetworkPublicKeyHexed(os.Getenv("NETWORK_PUBLIC_KEY")),
		ProviderPrivateKey: network.PrivateKeyHexed(os.Getenv("PROVIDER_PRIVATE_KEY")),
		TZeroEndpoint:      os.Getenv("TZERO_ENDPOINT"),
		ServerAddr:         ":" + os.Getenv("PORT"),
	}
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
	providerServiceHandler, err := provider.NewHttpHandler(
		config.NetworkPublicKey,
		provider.Handler(paymentconnect.NewProviderServiceHandler,
			paymentconnect.ProviderServiceHandler(handler.NewProviderServiceImplementation(networkClient))),
	)
	if err != nil {
		log.Fatalf("Failed to create provider service handler: %v", err)
	}

	shutdownFunc, err := provider.StartServer(
		providerServiceHandler,
		provider.WithAddr(config.ServerAddr),
	)
	if err != nil {
		log.Fatalf("Failed to start provider server: %v", err)
	}

	log.Printf("✅ Step 1.1: Provider server initialized on %s\n", config.ServerAddr)

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
