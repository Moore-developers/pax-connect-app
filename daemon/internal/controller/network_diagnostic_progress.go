package controller

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/measurement"
)

func validDiagnosticOperation(id string) bool {
	if len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func (p *pathDiagnosticsOwner) readProgress(id string) (*pb.NetworkDiagnosticReport, error) {
	p.progressMu.Lock()
	defer p.progressMu.Unlock()
	if p.progress == nil || p.progress.OperationId != id {
		return nil, status.Error(codes.NotFound, "diagnostic_progress_pending")
	}
	return &pb.NetworkDiagnosticReport{Progress: proto.Clone(p.progress).(*pb.NetworkDiagnosticProgress)}, nil
}

func (p *pathDiagnosticsOwner) beginProgress(id string) func(measurement.DiagnosticProgress) {
	p.progressMu.Lock()
	p.progress = &pb.NetworkDiagnosticProgress{OperationId: id, ExitState: "pending", SourcesState: "pending"}
	value := p.progress
	p.progressMu.Unlock()
	return func(update measurement.DiagnosticProgress) {
		p.progressMu.Lock()
		defer p.progressMu.Unlock()
		if p.progress != value || value.Finished {
			return
		}
		value.ProbesCompleted = max(value.ProbesCompleted, uint32(update.ProbesCompleted))
		value.ProbesTotal = max(value.ProbesTotal, uint32(update.ProbesTotal))
		if update.ExitState != "" {
			value.ExitState = update.ExitState
		}
		if update.SourcesState != "" {
			value.SourcesState = update.SourcesState
		}
	}
}

func (p *pathDiagnosticsOwner) finishProgress() {
	p.progressMu.Lock()
	defer p.progressMu.Unlock()
	if p.progress != nil {
		p.progress.Finished = true
	}
}
