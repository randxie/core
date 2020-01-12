package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/gorilla/handlers"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/onepanelio/core/api"
	"github.com/onepanelio/core/kube"
	"github.com/onepanelio/core/manager"
	"github.com/onepanelio/core/repository"
	"github.com/onepanelio/core/server"
	"github.com/pressly/goose"
	"github.com/tmc/grpc-websocket-proxy/wsproxy"
	"google.golang.org/grpc"
)

var (
	rpcPort  = flag.String("rpc-port", ":8887", "RPC Port")
	httpPort = flag.String("http-port", ":8888", "RPC Port")
)

func main() {
	flag.Parse()

	db := repository.NewDB(os.Getenv("DB_DRIVER_NAME"), os.Getenv("DB_DATASOURCE_NAME"))
	if err := goose.Run("up", db.Base(), "db"); err != nil {
		log.Fatalf("goose up: %v", err)
	}

	kubeClient := kube.NewClient(os.Getenv("KUBECONFIG"))

	go startRPCServer(db, kubeClient)
	startHTTPProxy()
}

func startRPCServer(db *repository.DB, kubeClient *kube.Client) {
	resourceManager := manager.NewResourceManager(db, kubeClient)

	log.Printf("Starting RPC server on port %v", *rpcPort)
	lis, err := net.Listen("tcp", *rpcPort)
	if err != nil {
		log.Fatalf("Failed to start RPC listener: %v", err)
	}

	s := grpc.NewServer(grpc.UnaryInterceptor(loggingInterceptor))
	api.RegisterWorkflowServiceServer(s, server.NewWorkflowServer(resourceManager))
	api.RegisterSecretServiceServer(s, server.NewSecretServer(resourceManager))

	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve RPC server: %v", err)
	}
}

func startHTTPProxy() {
	endpoint := "localhost" + *rpcPort
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register gRPC server endpoint
	// Note: Make sure the gRPC server is running properly and accessible
	mux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithInsecure()}

	err := api.RegisterWorkflowServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
	if err != nil {
		log.Fatalf("Failed to connect to service: %v", err)
	}

	err = api.RegisterSecretServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
	if err != nil {
		log.Fatalf("Failed to connect to service: %v", err)
	}

	log.Printf("Starting HTTP proxy on port %v", *httpPort)
	if err = http.ListenAndServe(*httpPort, wsproxy.WebsocketProxy(handlers.CORS()(mux))); err != nil {
		log.Fatalf("Failed to serve HTTP listener: %v", err)
	}
}

func loggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	log.Printf("%v handler started", info.FullMethod)
	resp, err = handler(ctx, req)
	if err != nil {
		log.Printf("%s call failed", info.FullMethod)
		return
	}
	log.Printf("%v handler finished", info.FullMethod)
	return
}