package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"sync"
	"time"
	"winterflow/internal/infra/transport/grpc/handler"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"winterflow/internal/infra/transport/grpc/proto"

	"google.golang.org/grpc"
	//"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	//"google.golang.org/grpc/status"
)

type Hub struct {
	agents     map[string]*agent
	agentsLock sync.RWMutex
	cfg        *config.Config
	log        *logger.Logger
	srv        *grpc.Server
}

type agent struct {
	streamActive bool
	lastSeen     time.Time
	metrics      map[string]string
	cancelFunc   context.CancelFunc                                                // To cancel active stream
	stream       grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand] // Reference to the active stream

	pendingRequests     map[string]interface{}
	pendingRequestsLock sync.RWMutex
}

func NewHub(log *logger.Logger, cfg *config.Config) *Hub {
	h := &Hub{
		agents: make(map[string]*agent),
		cfg:    cfg,
		log:    log,
		srv:    createServer(log, cfg),
	}

	return h
}

func createServer(log *logger.Logger, cfg *config.Config) *grpc.Server {
	caCert, err := os.ReadFile(cfg.GetHubCACertPath())
	if err != nil {
		log.Fatal("Failed to read CA certificate", "error", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		log.Fatal("Failed to append CA certificate to pool")
	}

	serverCert, err := tls.LoadX509KeyPair(cfg.GetHubCertPath(), cfg.GetHubKeyPath())
	if err != nil {
		log.Fatal("Failed to load server certificate and key", "error", err)
	}

	tlsConfig := &tls.Config{
		ClientAuth:   tls.RequireAndVerifyClientCert,
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    certPool,
		MinVersion:   tls.VersionTLS12,
		// gRPC requires HTTP/2, so we must advertise it via ALPN.
		// Without this, some clients will send a ClientHello but abort after
		// receiving no suitable protocol selection, leading to mysterious
		// "connection reset" / TransientFailure errors.
		NextProtos: []string{"h2"},
	}

	log.Info("TLS configuration", "client_auth", tlsConfig.ClientAuth, "ca_cert_path", cfg.GetHubCACertPath())
	log.Info("Hub certificate loaded", "cert", cfg.GetHubCertPath(), "key", cfg.GetHubKeyPath())

	creds := credentials.NewTLS(tlsConfig)
	srv := grpc.NewServer(
		grpc.Creds(creds),
		grpc.StatsHandler(handler.NewLogStatsHandler(log)),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:                  15 * time.Second,
			Timeout:               5 * time.Second,
			MaxConnectionIdle:     30 * time.Minute,
			MaxConnectionAge:      2 * time.Hour,
			MaxConnectionAgeGrace: 5 * time.Minute,
		}),
	)
	//proto.RegisterAgentServiceServer(srv, h)

	return srv
}

func (h *Hub) ListenAndServe(ctx context.Context) error {
	lis, err := net.Listen("tcp", ":"+h.cfg.GetHubPort())
	if err != nil {
		h.log.Fatal("Failed to listen", "error", err)
	}

	h.log.Info("gRPC server listening", "port", h.cfg.GetHubPort())
	if err := h.srv.Serve(lis); err != nil {
		h.log.Error("Failed to serve", "error", err)
	}

	return nil
}

func (h *Hub) Shutdown() error {
	h.log.Info("Shutting down gRPC server")

	h.agentsLock.Lock()
	for id, server := range h.agents {
		if server.streamActive && server.cancelFunc != nil {
			h.log.Info("Cancelling active stream", "agent_id", id)
			server.cancelFunc()
		}
	}
	h.agentsLock.Unlock()

	time.Sleep(2 * time.Second)

	h.srv.GracefulStop()
	return nil
}
