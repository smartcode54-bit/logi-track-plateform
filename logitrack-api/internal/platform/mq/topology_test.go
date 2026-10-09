package mq_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

func TestCommittedDefinitionsAreUpToDate(t *testing.T) {
	want, err := mq.DefinitionsJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../../deploy/rabbitmq-definitions.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("deploy/rabbitmq-definitions.json is stale: run `go generate ./internal/platform/mq`")
	}
}

func TestTopologyMatchesAppendixB(t *testing.T) {
	if len(mq.Queues) != 16 {
		t.Fatalf("Appendix B §B.5.3 lists 16 work queues, got %d", len(mq.Queues))
	}
	if len(mq.RetryDelays) != mq.MaxAttempts {
		t.Fatalf("one retry rung per attempt: %d rungs for %d attempts", len(mq.RetryDelays), mq.MaxAttempts)
	}
	d := mq.BuildDefinitions()
	queues := map[string]map[string]any{}
	for _, q := range d.Queues {
		queues[q.Name] = q.Arguments
	}
	for _, q := range mq.Queues {
		args, ok := queues[q.Name]
		if !ok {
			t.Fatalf("missing queue %s", q.Name)
		}
		if args["x-queue-type"] != "quorum" || args["x-dead-letter-exchange"] != mq.ExchangeDLX || args["x-dead-letter-routing-key"] != q.Name {
			t.Fatalf("%s args = %v", q.Name, args)
		}
		if _, ok := queues[mq.DeadQueue(q.Name)]; !ok {
			t.Fatalf("missing dead queue for %s", q.Name)
		}
	}
	for _, r := range mq.RetryDelays {
		args := queues[mq.RetryQueue(r.Label)]
		if args["x-dead-letter-exchange"] != mq.ExchangeRequeue || args["x-message-ttl"] != r.TTL.Milliseconds() {
			t.Fatalf("retry queue %s args = %v", r.Label, args)
		}
		if _, hasKey := args["x-dead-letter-routing-key"]; hasKey {
			t.Fatalf("retry queue %s must keep the original routing key", r.Label)
		}
	}
	if queues["cartrack.sync"]["x-single-active-consumer"] != true {
		t.Fatal("cartrack.sync must be single-active-consumer")
	}
}

// TestRequeueBindingsAreQueueSpecific simulates topic matching for the retry path:
// a message retried from billing.compute must come back to billing.compute
// only, even though other queues share routing keys on lt.events.
func TestRequeueBindingsAreQueueSpecific(t *testing.T) {
	d := mq.BuildDefinitions()
	retried := "1m.billing.compute"
	var dest []string
	for _, b := range d.Bindings {
		if b.Source == mq.ExchangeRequeue && topicMatch(b.RoutingKey, retried) {
			dest = append(dest, b.Destination)
		}
	}
	if strings.Join(dest, ",") != "billing.compute" {
		t.Fatalf("requeue of %s reaches %v", retried, dest)
	}
	var retry []string
	for _, b := range d.Bindings {
		if b.Source == mq.ExchangeRetry && topicMatch(b.RoutingKey, retried) {
			retry = append(retry, b.Destination)
		}
	}
	if strings.Join(retry, ",") != "retry.1m" {
		t.Fatalf("retry of %s reaches %v", retried, retry)
	}
}

// topicMatch implements AMQP topic matching (* = one word, # = zero or more).
func topicMatch(pattern, key string) bool {
	return match(strings.Split(pattern, "."), strings.Split(key, "."))
}

func match(p, k []string) bool {
	if len(p) == 0 {
		return len(k) == 0
	}
	switch p[0] {
	case "#":
		for i := 0; i <= len(k); i++ {
			if match(p[1:], k[i:]) {
				return true
			}
		}
		return false
	case "*":
		return len(k) > 0 && match(p[1:], k[1:])
	default:
		return len(k) > 0 && p[0] == k[0] && match(p[1:], k[1:])
	}
}
