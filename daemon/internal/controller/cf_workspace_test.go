package controller

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/network"
	"sync"
	"testing"
	"time"
)

type memoryCFFile struct {
	mu   sync.Mutex
	data []byte
}

func (m *memoryCFFile) Read() ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.data...), len(m.data) > 0, nil
}
func (m *memoryCFFile) Write(_ context.Context, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = append([]byte(nil), b...)
	return nil
}
func TestCFWorkspaceRecordsSurviveReloadButDoNotBecomeProof(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "network-A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	file := &memoryCFFile{}
	c.ConfigureCFWorkspace(file, func(network.Physical) cf.Probe {
		return cf.Probe{TCP: func(context.Context, string) (time.Duration, error) { return 100 * time.Millisecond, nil }}
	})
	initial, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(initial.Records) != 0 {
		t.Fatal("opening fabricated or scanned candidates", initial, err)
	}
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "check", Ips: []string{"104.16.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("check failed")
	}
	c.ConfigureCFWorkspace(file, func(network.Physical) cf.Probe { return cf.Probe{} })
	restored, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(restored.Records) != 1 {
		t.Fatal(restored, err)
	}
	if restored.Records[0].Tcp.Value != 100 || restored.Records[0].Tcp.MeasuredAt == nil || restored.Records[0].ProofCurrent {
		t.Fatal("history lost or became route proof", restored)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestCFAlternateCandidateProofMatchesOriginalDraftAndCorruptionSurvives(t *testing.T) {
	c := newTestController(t, &fakeTester{duration: 150 * time.Millisecond})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	file := &memoryCFFile{}
	c.ConfigureCFWorkspace(file, func(network.Physical) cf.Probe { return cf.Probe{} })
	sink, stop := operationSink(c)
	defer stop()
	raw := `{"type":"trojan","tag":"CF","server":"104.16.0.1","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"identity.test"},"transport":{"type":"ws","path":"/ws","headers":{"Host":"identity.test"}}}`
	ack, err := c.AddRoute(context.Background(), &pb.RouteEditRequest{DocumentJson: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("add")
	}
	node := waitComplete(t, c).Routes[0]
	request := &pb.CFValidationRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: raw, Ip: "104.16.0.2"}
	result, err := c.ValidateCFCandidate(context.Background(), request)
	if err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
	workspace, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: raw})
	if err != nil || len(workspace.Records) != 1 || !workspace.Records[0].ProofCurrent {
		t.Fatal("alternate-IP proof lost", workspace, err)
	}
	corrupt := &memoryCFFile{data: []byte("not valid JSON")}
	c.ConfigureCFWorkspace(corrupt, func(network.Physical) cf.Probe { return cf.Probe{} })
	_, err = c.ValidateCFCandidate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	corrupt.mu.Lock()
	unchanged := string(corrupt.data) == "not valid JSON"
	corrupt.mu.Unlock()
	if !unchanged {
		t.Fatal("corrupt evidence overwritten")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestCFWorkspaceHidesOtherNetworkCandidatesWithoutMonitoring(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "new"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	d := cf.NewDocument()
	d.Merge(cf.Record{IP: "104.16.0.1", Network: "old", Reachable: true, TCP: cf.Sample{Value: 100, At: time.Now(), Attempt: time.Now()}}, time.Now())
	raw, err := cf.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	c.ConfigureCFWorkspace(&memoryCFFile{data: raw}, func(network.Physical) cf.Probe { return cf.Probe{} })
	w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(w.GetRecords()) != 0 {
		t.Fatal("old network candidates leaked into current list", w, err)
	}
	if _, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "monitor_start"}); err == nil {
		t.Fatal("removed continuous monitoring is still accepted")
	}
}

func TestCFSingleDownloadChecksOnlyRequestedIPAndContinuesInSameTask(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	var mu sync.Mutex
	tcpIPs := []string{}
	downloadIPs := []string{}
	c.ConfigureCFWorkspace(&memoryCFFile{}, func(network.Physical) cf.Probe {
		return cf.Probe{
			TCP: func(_ context.Context, ip string) (time.Duration, error) {
				mu.Lock()
				tcpIPs = append(tcpIPs, ip)
				mu.Unlock()
				return 50 * time.Millisecond, nil
			},
			Download: func(_ context.Context, ip string) (float64, error) {
				mu.Lock()
				downloadIPs = append(downloadIPs, ip)
				mu.Unlock()
				return 1024 * 1024, nil
			},
		}
	})
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "download_one", Ips: []string{"104.16.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tcpIPs) != 3 || len(downloadIPs) != 1 {
		t.Fatal("single download did not run bounded TCP check and one HTTP sample", tcpIPs, downloadIPs)
	}
	for _, ip := range append(tcpIPs, downloadIPs...) {
		if ip != "104.16.0.1" {
			t.Fatal("tested unrelated IP", ip)
		}
	}
	workspace, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(workspace.Records) != 1 || workspace.Records[0].Download.Value != 1024*1024 {
		t.Fatal(workspace, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCFSingleDownloadFailureDoesNotDownloadOrTouchOtherIPs(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	c.ConfigureCFWorkspace(&memoryCFFile{}, func(network.Physical) cf.Probe {
		return cf.Probe{
			TCP:      func(context.Context, string) (time.Duration, error) { return 0, errors.New("fixture unreachable") },
			Download: func(context.Context, string) (float64, error) { t.Error("unreachable IP downloaded"); return 0, nil },
		}
	})
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "download_one", Ips: []string{"104.16.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if result.Ok || result.Error.GetCode() != "cf_tcp_unreachable" {
		t.Fatal(result)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCFSingleDownloadCanCancelDuringCheck(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	entered := make(chan struct{})
	var once sync.Once
	c.ConfigureCFWorkspace(&memoryCFFile{}, func(network.Physical) cf.Probe {
		return cf.Probe{TCP: func(ctx context.Context, _ string) (time.Duration, error) {
			once.Do(func() { close(entered) })
			<-ctx.Done()
			return 0, ctx.Err()
		}, Download: func(context.Context, string) (float64, error) { t.Error("cancelled download started"); return 0, nil }}
	})
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "download_one", Ips: []string{"104.16.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if result.Error.GetCode() != "operation_cancelled" {
		t.Fatal(result)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCFLocationPersistsWithoutErasingPreviousNetworkMetrics(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "new"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
	c.ConfigureCFCandidates()
	d := cf.NewDocument()
	d.Merge(cf.Record{IP: "104.16.0.1", Network: "old", TCP: cf.Sample{Value: 100, At: time.Now(), Attempt: time.Now()}}, time.Now())
	raw, err := cf.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	file := &memoryCFFile{data: raw}
	calls := 0
	probe := func(network.Physical) cf.Probe {
		return cf.Probe{Locate: func(_ context.Context, ip string) (cf.Location, error) {
			calls++
			if ip != "104.16.0.1" {
				t.Error("hidden IP looked up")
			}
			return cf.Location{Country: "United States", Region: "California", City: "San Francisco", At: time.Now(), Source: "fixture"}, nil
		}}
	}
	c.ConfigureCFWorkspace(file, probe)
	sink, stop := operationSink(c)
	defer stop()
	for i := 0; i < 2; i++ {
		ack, e := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "locate", Ips: []string{"104.16.0.1"}})
		if e != nil {
			t.Fatal(e)
		}
		if !awaitResult(t, sink, ack.OperationId).Ok {
			t.Fatal("location failed")
		}
	}
	c.ConfigureCFWorkspace(file, probe)
	report, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(report.GetRecords()) != 1 {
		t.Fatal(report, err)
	}
	row := report.Records[0]
	if calls != 1 || row.Tcp.Value != 0 || row.Network != "new" || row.GetLocation().GetCity() != "San Francisco" || row.ProofCurrent {
		t.Fatal("location inherited old network metrics", report, calls)
	}
}

func TestCFDisplayedDownloadIncludesAll25RequestedIPs(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
	c.ConfigureCFCandidates()
	var mu sync.Mutex
	downloaded := map[string]int{}
	c.ConfigureCFWorkspace(&memoryCFFile{}, func(network.Physical) cf.Probe {
		return cf.Probe{TCP: func(context.Context, string) (time.Duration, error) { return time.Millisecond, nil }, Download: func(_ context.Context, ip string) (float64, error) {
			mu.Lock()
			downloaded[ip]++
			mu.Unlock()
			return 1000, nil
		}}
	})
	ips := []string{}
	for i := 1; i <= 25; i++ {
		ips = append(ips, fmt.Sprintf("104.16.0.%d", i))
	}
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "download", Ips: ips})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("displayed batch failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(downloaded) != 25 {
		t.Fatal("truncated to old top-5/20 or included hidden IPs", downloaded)
	}
	for _, ip := range ips {
		if downloaded[ip] != 1 {
			t.Fatal("download should sample each IP once", downloaded)
		}
	}
}

func TestCFCheckNeverStartsDownload(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
	c.ConfigureCFCandidates()
	c.ConfigureCFWorkspace(&memoryCFFile{}, func(network.Physical) cf.Probe {
		return cf.Probe{
			TCP: func(context.Context, string) (time.Duration, error) { return time.Millisecond, nil },
			Download: func(context.Context, string) (float64, error) {
				t.Error("latency task started a download")
				return 0, nil
			},
		}
	})
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "check", Ips: []string{"104.16.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("latency failed")
	}
	report, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(report.GetRecords()) != 1 || report.Records[0].Tcp.Value <= 0 || report.Records[0].Download.AttemptedAt != nil {
		t.Fatal("latency altered download sample", report, err)
	}
}

func TestCFDownloadPublishesFirstResultWhileNextIPIsBlocked(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			c := newTestController(t, &fakeTester{})
			c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
				return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
			}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
			c.ConfigureCFCandidates()
			file := &memoryCFFile{}
			nextStarted := make(chan struct{})
			first := "104.16.0.1"
			c.ConfigureCFWorkspace(file, func(network.Physical) cf.Probe {
				return cf.Probe{
					TCP: func(context.Context, string) (time.Duration, error) { return time.Millisecond, nil },
					Download: func(ctx context.Context, ip string) (float64, error) {
						if ip != first {
							close(nextStarted)
							<-ctx.Done()
							return 0, ctx.Err()
						}
						<-nextStarted
						if fail {
							return 0, errors.New("fixture failure")
						}
						return 1000, nil
					},
				}
			})
			sink, stop := operationSink(c)
			defer stop()
			ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "download", Ips: []string{"104.16.0.1", "104.16.0.2"}})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-nextStarted:
			case <-time.After(2 * time.Second):
				t.Fatal("next IP did not begin")
			}
			var report *pb.CFWorkspace
			deadline := time.Now().Add(time.Second)
			for {
				report, err = c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
				if err != nil {
					t.Fatal(err)
				}
				published := false
				for _, row := range report.Records {
					if row.Ip == first && row.Download.AttemptedAt != nil {
						published = true
					}
				}
				if published || time.Now().After(deadline) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err != nil || report.GetOperationId() != ack.OperationId {
				t.Fatal("batch must still run", report, err)
			}
			found := false
			for _, row := range report.Records {
				if row.Ip == first {
					found = true
					if row.Download.AttemptedAt == nil {
						t.Error("completed IP has no published outcome while later IP is running")
					}
					if !fail && (row.Download.Value != 1000 || row.Download.SuccessfulRounds != 1) {
						t.Error("successful IP not visible", row.Download)
					}
					if fail && row.Download.ErrorCode != "cf_download_incomplete" {
						t.Error("failed IP did not publish failure", row.Download)
					}
				}
			}
			if !found {
				t.Error("missing first IP")
			}
			if _, err = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
				t.Fatal(err)
			}
			awaitResult(t, sink, ack.OperationId)
			c.ConfigureCFWorkspace(file, func(network.Physical) cf.Probe { return cf.Probe{} })
			restored, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range restored.Records {
				if row.Ip == first && row.Download.AttemptedAt == nil {
					t.Fatal("cancel/reload erased completed outcome")
				}
			}
		})
	}
}

func TestCFDownloadRejectedTCPPublishesFailureBeforeOtherDownloadEnds(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
	c.ConfigureCFCandidates()
	d := cf.NewDocument()
	d.Merge(cf.Record{IP: "104.16.0.1", Network: "A", Download: cf.Sample{Value: 1500, At: time.Now().Add(-time.Hour), Attempt: time.Now().Add(-time.Hour)}}, time.Now())
	raw, err := cf.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	c.ConfigureCFWorkspace(&memoryCFFile{data: raw}, func(network.Physical) cf.Probe {
		return cf.Probe{
			TCP: func(_ context.Context, ip string) (time.Duration, error) {
				if ip == "104.16.0.1" {
					return 0, errors.New("unreachable")
				}
				return time.Millisecond, nil
			},
			Download: func(ctx context.Context, ip string) (float64, error) {
				if ip == "104.16.0.1" {
					t.Error("unreachable IP downloaded")
				}
				close(entered)
				<-ctx.Done()
				return 0, ctx.Err()
			},
		}
	})
	sink, stop := operationSink(c)
	defer stop()
	started := time.Now()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "download", Ips: []string{"104.16.0.1", "104.16.0.2"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("download not running")
	}
	report, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range report.Records {
		if row.Ip == "104.16.0.1" {
			if row.Download.ErrorCode != "cf_tcp_unreachable" || row.Download.AttemptedAt == nil || row.Download.AttemptedAt.AsTime().Before(started) || row.Download.Value != 1500 {
				t.Error("precheck failure missing or erased history", row.Download)
			}
		}
	}
	if _, err = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	awaitResult(t, sink, ack.OperationId)
}

func TestCFDownloadUsesConfiguredParallelismAndOneSample(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	prefs, err := c.GetMeasurementPreferences(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	prefs.DownloadMode = "manual"
	prefs.DownloadParallel = 2
	if _, err = c.SaveMeasurementPreferences(context.Background(), prefs); err != nil {
		t.Fatal(err)
	}
	c.ConfigureCFCandidates()
	started := make(chan string, 3)
	release := make(chan struct{})
	var mu sync.Mutex
	calls := map[string]int{}
	active, peak := 0, 0
	c.ConfigureCFWorkspace(&memoryCFFile{}, func(network.Physical) cf.Probe {
		return cf.Probe{
			TCP: func(context.Context, string) (time.Duration, error) { return time.Millisecond, nil },
			Download: func(ctx context.Context, ip string) (float64, error) {
				mu.Lock()
				calls[ip]++
				first := calls[ip] == 1
				active++
				peak = max(peak, active)
				mu.Unlock()
				defer func() { mu.Lock(); active--; mu.Unlock() }()
				if first {
					select {
					case started <- ip:
					case <-ctx.Done():
						return 0, ctx.Err()
					}
				}
				select {
				case <-release:
					return 1000, nil
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			},
		}
	})
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "download", Ips: []string{"104.16.0.1", "104.16.0.2", "104.16.0.3"}})
	if err != nil {
		t.Fatal(err)
	}
	defer close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("configured two-way download did not start in parallel")
		}
	}
	// Saving a new limit affects the next task, not the admitted task's workers.
	prefs, err = c.GetMeasurementPreferences(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	prefs.DownloadParallel = 1
	if _, err = c.SaveMeasurementPreferences(context.Background(), prefs); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("admitted task changed worker count")
	}
	release <- struct{}{}
	release <- struct{}{}
	if !awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("download failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != 2 || len(calls) != 3 {
		t.Fatal("download did not obey limit", peak, calls)
	}
	for _, count := range calls {
		if count != 1 {
			t.Fatal("CF download still repeats samples", calls)
		}
	}
}
