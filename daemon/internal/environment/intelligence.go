package environment

import (
	"context"

	"pax-connect/daemon/internal/measurement"
)

// One environment round shares exact-IP database work, never its IP/DNS/ICE probes.
// Results are immutable after done closes; waiters remain cancelable.
type intelligenceTask struct {
	done   chan struct{}
	report measurement.NetworkDiagnosticReport
}

func (s *Session) intelligence(ctx context.Context, ip string) measurement.NetworkDiagnosticReport {
	s.mu.Lock()
	if s.intelligenceTasks == nil {
		s.intelligenceTasks = make(map[string]*intelligenceTask)
	}
	if task := s.intelligenceTasks[ip]; task != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return measurement.NetworkDiagnosticReport{ProviderError: "operation_cancelled"}
		case <-task.done:
			return task.report
		}
	}
	task := &intelligenceTask{done: make(chan struct{})}
	s.intelligenceTasks[ip] = task
	s.mu.Unlock()
	task.report = s.lookup.DiagnoseIP(ctx, s.client, ip, "environment/"+s.Read().ID)
	close(task.done)
	return task.report
}
