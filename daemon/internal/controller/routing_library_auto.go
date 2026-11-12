package controller

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"time"
)

// One lifetime-owned worker downloads only subscribed packages whose user opted
// into daily updates. UI visibility never starts a polling loop.
func (c *Controller) startRoutingAutomatic() {
	o := c.routing
	if o == nil || c.proxy == nil || o.autoDone != nil {
		return
	}
	o.autoJobs = make(chan *pb.RoutingMutationRequest, 1)
	o.autoDone = make(chan struct{})
	o.lastAttempt = map[string]time.Time{}
	o.updateErrors = map[string]string{}
	go func() {
		defer close(o.autoDone)
		for {
			select {
			case <-c.proxy.lifetime.Done():
				return
			case q := <-o.autoJobs:
				ctx, done := context.WithTimeout(c.proxy.lifetime, 35*time.Second)
				eligible := false
				if err := c.query(ctx, func(context.Context) {
					for _, p := range o.library.Packages {
						if p.ID == q.PackageId && p.Automatic && p.CanUpdate() {
							eligible = true
							q.ExpectedRevision = o.library.Revision
						}
					}
				}); err != nil {
					done()
					continue
				}
				if !eligible {
					done()
					continue
				}
				_, err := c.MutateRoutingLibrary(ctx, q)
				if err != nil && ctx.Err() == nil {
					_ = c.query(ctx, func(context.Context) {
						if status.Code(err) == codes.Aborted {
							o.lastAttempt[q.PackageId] = time.Now().Add(-24*time.Hour + 5*time.Minute)
						} else {
							o.updateErrors[q.PackageId] = "自动更新未完成，保留上一有效版本。"
						}
					})
				}
				done()
			}
		}
	}()
}

// Called only by the existing product owner; scheduling state has no second writer.
func (c *Controller) scheduleRoutingAutomatic() {
	o := c.routing
	if o == nil || o.autoDone == nil || o.loadError != nil {
		return
	}
	for _, p := range o.library.Packages {
		if !p.CanUpdate() || !p.Automatic || time.Since(time.Unix(p.LastChecked, 0)) < 24*time.Hour || time.Since(o.lastAttempt[p.ID]) < 24*time.Hour {
			continue
		}
		q := &pb.RoutingMutationRequest{Action: "update", PackageId: p.ID, ExpectedRevision: o.library.Revision}
		select {
		case o.autoJobs <- q:
			o.lastAttempt[p.ID] = time.Now()
		default:
		}
		return
	}
}
