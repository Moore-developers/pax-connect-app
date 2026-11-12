package controller

import (
	"crypto/rand"
	"encoding/hex"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/stats"
	"time"
)

// A frozen read belongs to one read cache: traffic tokens stay on the legacy owner,
// log tokens on their own cache (history-storage §4.2).
type historyRead struct {
	logVersion                                 *logModelVersion
	aggregateMemberKeys                        map[string]string
	aggregateDirection                         string
	aggregateConnectionRows                    map[string][]*logModelConnection
	aggregateFlows                             []stats.FlowWindow
	aggregateFlowRows                          map[string][]stats.FlowWindow
	ruleMemberBuckets                          map[string]map[int64]bool
	recentResponses                            map[string][]*pb.TrafficResponsePoint
	recentResponsesState, recentResponsesError string
	logEvidence                                bool
	logJournalGeneration                       string
	userApplications                           map[string]config.RoutingApplication
	aggregateCoverageRows                      map[int64]aggregateCoverage
	aggregate                                  bool
	window                                     string
	aggregateCoverage                          time.Time
	aggregateTrend                             map[string][]*pb.TrafficPoint
	dimension                                  string
	at, from, until, timeUsed                  time.Time
	createdAt, detailAt                        time.Time
	kind, signature                            string
	logs                                       []config.HistoryEvent
	problemLogs                                []config.HistoryEvent
	records                                    []*pb.LogRecord
	recordByID                                 map[string]*pb.LogRecord
	recordEvents                               map[string][]string
	rows                                       []*pb.TrafficRow
	dns, unknown                               *pb.TrafficRow
	flows                                      map[string][]*pb.FlowRecord
	trend                                      []*pb.TrafficPoint
	upload, download, connections              uint64
	// System-service bytes shown beside the application subtotal, which excludes them.
	systemUpload, systemDownload                                      uint64
	otherProcessUpload, otherProcessDownload, otherProcessConnections uint64
	meta                                                              historyMetadata
	// Generations observed when a log page was built; the log cache refuses to freeze
	// a page across a clear or summary rebuild.
	deletionGeneration uint64
	summaryGeneration  string
	diagnosticsHead    int64
	bytes              int
}

func historyToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
