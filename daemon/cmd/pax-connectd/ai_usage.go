package main

import (
	"context"
	"os"
	"time"

	"google.golang.org/grpc"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiprofile"
	"pax-connect/daemon/internal/aiusage"
	"pax-connect/daemon/internal/controller"
)

func userAIUsage(ctx context.Context, directory string, profiles *aiprofile.Manager, server *grpc.Server) (*aiusage.Store, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	roots := map[string]string{}
	for env, cli := range map[string]string{"CLAUDE_CONFIG_DIR": "claude", "CODEX_HOME": "codex", "DSH_HOME": "dsh"} {
		if v := os.Getenv(env); v != "" {
			roots[cli] = v
		}
	}

	s, e := aiusage.Open(ctx, aiusage.Options{Directory: directory, Home: home, Owner: uint32(os.Geteuid()), Roots: roots, Timeline: func(ctx context.Context) (aiusage.Resolver, error) {
		t, e := profiles.Timeline(ctx)
		if e != nil {
			return nil, e
		}
		return func(cli string, at time.Time) aiusage.Attribution {
			a := t.Resolve(aiprofile.CLI(cli), at)
			return aiusage.Attribution{ProfileID: a.ProfileID, ProfileName: a.ProfileName, Multiplier: a.Multiplier, Models: a.Models, ServiceSource: a.ServiceSource, Since: a.Since}
		}, nil
	}})
	if e != nil {
		return nil, e
	}
	if e = s.Configure(ctx); e != nil {
		_ = s.Close(ctx)
		return nil, e
	}
	pb.RegisterAIUsageServiceServer(server, controller.NewAIUsageServer(s, profiles))
	return s, nil
}
