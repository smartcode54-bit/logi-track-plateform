// Package mq holds the RabbitMQ topology of Appendix B §B.5 as data. The same
// table produces deploy/rabbitmq-definitions.json (loaded by compose) and, from
// issue T10, is asserted idempotently by the worker at start.
package mq

import (
	"encoding/json"
	"time"
)

//go:generate go run ./gen -out ../../../deploy/rabbitmq-definitions.json

// Exchanges (Appendix B §B.5.1).
const (
	ExchangeEvents  = "lt.events"  // topic: domain events {aggregate}.{event}, published by the outbox relay
	ExchangeJobs    = "lt.jobs"    // direct: commands job.{type}, published by the outbox relay
	ExchangeRetry   = "lt.retry"   // topic: delayed retries {delay}.{queue}, published by the worker
	ExchangeRequeue = "lt.requeue" // topic: retry queues dead-letter here; binding *.{queue}
	ExchangeDLX     = "lt.dlx"     // direct: terminal failures, routing key = queue name
)

// MaxAttempts is the number of retries before a message is dead-lettered
// (R22, R54): five delayed retries, the sixth failure goes to {queue}.dead.
const MaxAttempts = 5

// Exchange describes one exchange.
type Exchange struct {
	Name string
	Kind string // topic | direct
}

// Exchanges lists every exchange in declaration order.
var Exchanges = []Exchange{
	{ExchangeEvents, "topic"},
	{ExchangeJobs, "direct"},
	{ExchangeRetry, "topic"},
	{ExchangeRequeue, "topic"},
	{ExchangeDLX, "direct"},
}

// RetryDelay is one rung of the retry ladder (Appendix B §B.5.4).
type RetryDelay struct {
	Label string // routing-key prefix and queue suffix: retry.{Label}
	TTL   time.Duration
}

// RetryDelays are used in order: attempt n waits RetryDelays[n-1].
var RetryDelays = []RetryDelay{
	{"10s", 10 * time.Second},
	{"1m", time.Minute},
	{"5m", 5 * time.Minute},
	{"30m", 30 * time.Minute},
	{"2h", 2 * time.Hour},
}

// RetryQueue returns the delay queue name for a label.
func RetryQueue(label string) string { return "retry." + label }

// DeadQueue returns the terminal queue of a work queue.
func DeadQueue(queue string) string { return queue + ".dead" }

// Binding attaches a work queue to an exchange with a routing key.
type Binding struct {
	Exchange string
	Key      string
}

// Queue is a durable quorum work queue (Appendix B §B.5.3).
type Queue struct {
	Name                 string
	Group                string // worker group that consumes it
	Prefetch             int
	SingleActiveConsumer bool
	Bindings             []Binding
}

func events(keys ...string) []Binding { return bind(ExchangeEvents, keys) }
func jobs(keys ...string) []Binding   { return bind(ExchangeJobs, keys) }

func bind(exchange string, keys []string) []Binding {
	out := make([]Binding, len(keys))
	for i, k := range keys {
		out[i] = Binding{Exchange: exchange, Key: k}
	}
	return out
}

func join(parts ...[]Binding) []Binding {
	var out []Binding
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Queues lists the 16 work queues of Appendix B §B.5.3.
var Queues = []Queue{
	{Name: "billing.compute", Group: "billing", Prefetch: 8, Bindings: join(
		events("trip.delivered", "trip.delivered_at_changed", "task.plan_date_changed", "standby.completed", "standby.customer_assigned"),
		jobs("job.billing.safety-net"))},
	{Name: "billing.backfill", Group: "billing", Prefetch: 1, Bindings: jobs(
		"job.billing.backfill-trips", "job.billing.backfill-standby", "job.billing.impact-report")},
	{Name: "notify.fcm", Group: "notify", Prefetch: 16, Bindings: events(
		"task.assigned", "task.reassigned", "task.cancelled", "task.updated", "task.checked_in", "task.plan_date_changed",
		"chat.message_created", "broadcast.created", "maintenance.created", "maintenance.reminder_requested",
		"leave.decided", "user.sessions_revoked")},
	{Name: "notify.line", Group: "notify", Prefetch: 4, Bindings: join(
		events("task.checked_in", "trip.delivered", "standby.completed"),
		jobs("job.notify.line.force"))},
	{Name: "notify.email", Group: "notify", Prefetch: 4, Bindings: join(
		events("auth.password_reset_requested", "user.created", "user.invited"),
		jobs("job.notify.email"))},
	{Name: "documents.render", Group: "documents", Prefetch: 2, Bindings: join(
		events("statement.created", "statement.status_changed"),
		jobs("job.documents.regenerate", "job.documents.shopee-report"))},
	{Name: "images.thumbnail", Group: "documents", Prefetch: 4, Bindings: events("storage.object_committed")},
	{Name: "cartrack.sync", Group: "integrations", Prefetch: 1, SingleActiveConsumer: true, Bindings: jobs("job.cartrack.sync")},
	{Name: "bangchak.snapshot", Group: "integrations", Prefetch: 1, Bindings: jobs("job.bangchak.snapshot")},
	{Name: "distances.compute", Group: "integrations", Prefetch: 1, Bindings: jobs("job.distances.compute")},
	{Name: "payroll.run", Group: "hr", Prefetch: 1, Bindings: jobs("job.payroll.run")},
	{Name: "security.audit", Group: "platform", Prefetch: 8, Bindings: events("security.event")},
	{Name: "tenancy.orphan-scan", Group: "platform", Prefetch: 1, Bindings: jobs("job.tenancy.orphan-scan")},
	{Name: "storage.gc", Group: "platform", Prefetch: 1, Bindings: jobs("job.storage.gc")},
	{Name: "etl.sync", Group: "sync", Prefetch: 1, Bindings: jobs("job.etl.sync")},
	{Name: "firestore.shadow", Group: "sync", Prefetch: 4, Bindings: events(
		"hubs.changed", "customers.changed", "companies.changed", "truck.created", "truck.updated",
		"assignment.created", "assignment.revoked", "driver.created", "driver.updated", "tenant.created",
		"tenant.updated", "holidays.changed", "broadcast.created", "settings.changed", "trip.priced", "standby.priced")},
}

// Definitions is the RabbitMQ definitions document (management HTTP API
// POST /api/definitions, vhost "/"). Users and permissions are not included:
// the broker's default user comes from RABBITMQ_DEFAULT_USER/PASS.
type Definitions struct {
	Exchanges []defExchange `json:"exchanges"`
	Queues    []defQueue    `json:"queues"`
	Bindings  []defBinding  `json:"bindings"`
}

type defExchange struct {
	Name       string         `json:"name"`
	VHost      string         `json:"vhost"`
	Type       string         `json:"type"`
	Durable    bool           `json:"durable"`
	AutoDelete bool           `json:"auto_delete"`
	Internal   bool           `json:"internal"`
	Arguments  map[string]any `json:"arguments"`
}

type defQueue struct {
	Name       string         `json:"name"`
	VHost      string         `json:"vhost"`
	Durable    bool           `json:"durable"`
	AutoDelete bool           `json:"auto_delete"`
	Arguments  map[string]any `json:"arguments"`
}

type defBinding struct {
	Source          string         `json:"source"`
	VHost           string         `json:"vhost"`
	Destination     string         `json:"destination"`
	DestinationType string         `json:"destination_type"`
	RoutingKey      string         `json:"routing_key"`
	Arguments       map[string]any `json:"arguments"`
}

const vhost = "/"

// BuildDefinitions turns the topology table into a definitions document.
func BuildDefinitions() Definitions {
	var d Definitions
	for _, e := range Exchanges {
		d.Exchanges = append(d.Exchanges, defExchange{
			Name: e.Name, VHost: vhost, Type: e.Kind, Durable: true, Arguments: map[string]any{},
		})
	}
	queue := func(name string, args map[string]any) {
		args["x-queue-type"] = "quorum"
		d.Queues = append(d.Queues, defQueue{Name: name, VHost: vhost, Durable: true, Arguments: args})
	}
	binding := func(exchange, dest, key string) {
		d.Bindings = append(d.Bindings, defBinding{
			Source: exchange, VHost: vhost, Destination: dest, DestinationType: "queue",
			RoutingKey: key, Arguments: map[string]any{},
		})
	}
	for _, q := range Queues {
		args := map[string]any{
			"x-dead-letter-exchange":    ExchangeDLX,
			"x-dead-letter-routing-key": q.Name,
		}
		if q.SingleActiveConsumer {
			args["x-single-active-consumer"] = true
		}
		queue(q.Name, args)
		queue(DeadQueue(q.Name), map[string]any{})
		for _, b := range q.Bindings {
			binding(b.Exchange, q.Name, b.Key)
		}
		binding(ExchangeRequeue, q.Name, "*."+q.Name)
		binding(ExchangeDLX, DeadQueue(q.Name), q.Name)
	}
	for _, r := range RetryDelays {
		// No dead-letter routing key: the original {delay}.{queue} key is kept,
		// so lt.requeue delivers to exactly one work queue.
		queue(RetryQueue(r.Label), map[string]any{
			"x-message-ttl":          r.TTL.Milliseconds(),
			"x-dead-letter-exchange": ExchangeRequeue,
		})
		binding(ExchangeRetry, RetryQueue(r.Label), r.Label+".#")
	}
	return d
}

// DefinitionsJSON renders BuildDefinitions as indented JSON with a trailing newline.
func DefinitionsJSON() ([]byte, error) {
	b, err := json.MarshalIndent(BuildDefinitions(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
