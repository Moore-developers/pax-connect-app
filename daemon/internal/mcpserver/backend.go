package mcpserver

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/ipc"
	"pax-connect/daemon/internal/mcpinstall"
	"pax-connect/daemon/internal/processidentity"
)

type backend struct {
	conn     *grpc.ClientConn
	rpc      pb.PaxServiceClient
	ai       pb.AIUsageServiceClient
	profiles pb.AIProfilesServiceClient
	hello    *pb.HelloResponse
}

func (s *Server) connect(ctx context.Context) (*backend, error) {
	path, fingerprint, err := s.target(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient("passthrough:///pax-user", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ipc.DialUser(ctx, path) }), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(8<<20)))
	if err != nil {
		return nil, errors.New("daemon_unavailable")
	}
	rpc := pb.NewPaxServiceClient(conn)
	hello, err := rpc.Hello(ctx, &pb.HelloRequest{ProtocolVersion: ipc.ProtocolVersion, ClientVersion: "pax-mcp/1"})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if hello.ProtocolVersion != ipc.ProtocolVersion || hello.DaemonInstance == "" {
		_ = conn.Close()
		return nil, errors.New("protocol_mismatch")
	}
	if !slicesContain(hello.Capabilities, "paxd.user") {
		_ = conn.Close()
		return nil, errors.New("app_mismatch")
	}
	if fingerprint != "" {
		owner, ownerErr := processidentity.Read(int(hello.OwnerAppPid))
		ownerPath, pathErr := processidentity.Executable(int(hello.OwnerAppPid))
		expectedOwner, canonicalErr := filepath.EvalSymlinks(filepath.Join(s.options.AppBundle, "Contents/MacOS/PaxConnect"))
		if s.options.AppBundle == "" {
			expectedOwner, canonicalErr = filepath.EvalSymlinks("/Applications/PaxConnect.app/Contents/MacOS/PaxConnect")
		}
		if ownerErr != nil || pathErr != nil || canonicalErr != nil || ownerPath != expectedOwner || !processidentity.Alive(owner) || owner.UID != uint32(os.Geteuid()) || hello.ServiceFingerprint != fingerprint {
			_ = conn.Close()
			return nil, errors.New("app_mismatch")
		}
	}
	return &backend{conn: conn, rpc: rpc, ai: pb.NewAIUsageServiceClient(conn), profiles: pb.NewAIProfilesServiceClient(conn), hello: hello}, nil
}

func slicesContain(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// Development may select a socket; production uses the shared signed-bundle boundary.
func (s *Server) target(ctx context.Context) (string, string, error) {
	if s.options.SocketPath != "" {
		return s.options.SocketPath, "", nil
	}
	bundle := s.options.AppBundle
	if bundle == "" {
		bundle = "/Applications/PaxConnect.app"
	}
	verified, err := mcpinstall.Verify(ctx, bundle)
	if err != nil {
		return "", "", err
	}
	if s.options.BundleCodeHash != "" && s.options.BundleCodeHash != verified.CodeHash {
		return "", "", errors.New("app_mismatch")
	}
	socket, err := verified.Socket(ctx)
	return socket, verified.Fingerprint, err
}
