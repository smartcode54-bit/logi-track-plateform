package orphanscan_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy/orphanscan"
)

type noPool struct{}

func (noPool) Begin(context.Context) (pgx.Tx, error) { return nil, context.Canceled }

// The consumer binds to the tenancy.orphan-scan queue of Appendix B §B.5.3, refuses another routing key and a command
// without a job id permanently, and registers its gauge once.
func TestRegistrationAndRefusals(t *testing.T) {
	if _, err := orphanscan.New(orphanscan.Deps{}); err == nil {
		t.Fatal("a job without a pool was built")
	}
	reg := prometheus.NewRegistry()
	j, err := orphanscan.New(orphanscan.Deps{Pool: noPool{}, Registerer: reg})
	if err != nil {
		t.Fatal(err)
	}
	if r := j.Registration(); r.Queue != orphanscan.Queue || r.Handler == nil || orphanscan.Route != "job."+orphanscan.JobType {
		t.Fatalf("registration %+v", r)
	}
	if _, err := orphanscan.New(orphanscan.Deps{Pool: noPool{}, Registerer: reg}); err == nil {
		t.Fatal("the gauge registered twice")
	}
	for name, d := range map[string]*mq.Delivery{
		"another key": {RoutingKey: "job.storage.gc", Body: []byte(`{"jobId":"01a12c05-c1fa-7443-b30a-e22f1be8ddd9"}`)},
		"no job id":   {RoutingKey: orphanscan.Route, Body: []byte(`{}`)},
		"not JSON":    {RoutingKey: orphanscan.Route, Body: []byte(`x`)},
	} {
		if err := j.Handle(context.Background(), d); !mq.IsPermanent(err) {
			t.Errorf("%s: %v, want a permanent failure", name, err)
		}
	}
}
