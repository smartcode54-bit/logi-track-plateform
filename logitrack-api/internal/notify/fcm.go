package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify/notifydb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/inbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push"
)

// QueueFCM is the work queue of the push consumer (Appendix B §B.5.3).
const QueueFCM = "notify.fcm"

// Routing keys bound to notify.fcm (Appendix B §B.5.3).
const (
	RouteTaskAssigned        = "task.assigned"
	RouteTaskReassigned      = "task.reassigned"
	RouteTaskCancelled       = "task.cancelled"
	RouteTaskUpdated         = "task.updated"
	RouteTaskCheckedIn       = "task.checked_in"
	RouteTaskPlanDateChanged = "task.plan_date_changed"
	RouteChatMessageCreated  = "chat.message_created"
	RouteBroadcastCreated    = "broadcast.created"
	RouteMaintenanceCreated  = "maintenance.created"
	RouteMaintenanceReminder = "maintenance.reminder_requested"
	RouteLeaveDecided        = "leave.decided"
	RouteSessionsRevoked     = "user.sessions_revoked"
)

// pendingRoutes are bound to notify.fcm but get their recipients from tables whose services land later
// (the issue named here). No producer emits them before that issue; one that did would see its message
// fail as transient and dead-letter after the last retry, to be replayed once the planner exists
// (POST /v1/admin/queues/notify.fcm/replay), instead of being acked and lost.
var pendingRoutes = map[string]string{
	RouteChatMessageCreated:  "T47",
	RouteBroadcastCreated:    "T48",
	RouteMaintenanceCreated:  "T40",
	RouteMaintenanceReminder: "T40",
	RouteLeaveDecided:        "T45",
}

// Delivery statuses (notification_deliveries.status). queued is never written: a row is inserted once
// its outcome is known.
const (
	StatusSent         = "sent"
	StatusFailed       = "failed"
	StatusTokenInvalid = "token_invalid"
	StatusDeduplicated = "deduplicated"
)

// Timing of the push consumer.
const (
	// TasksChangedWindow is the per-driver dedupe of the silent tasks_changed push (R21).
	TasksChangedWindow = 30 * time.Second
	// PushIdemTTL is the life of idem:fcm:{messageId}:{tokenId}: one message reaches one token at most
	// once within it (Appendix B §B.6.2).
	PushIdemTTL = 24 * time.Hour
	// InFlightStale is how long a claimed push may stay unfinished before another attempt of the same
	// message gives it up as interrupted (a worker died between the claim and the send's outcome); it is
	// well above fcmHandlerTimeout, so a live attempt is never taken over.
	InFlightStale = 5 * time.Minute

	// MaxDevicesPerDriver bounds the devices one driver push reaches: the most recently seen ones (the
	// cap registration keeps per user, auth.MaxDevicesPerUser; it also bounds ETL rows), so no user can
	// grow the fan-out of a message without bound.
	MaxDevicesPerDriver = 10

	fcmHandlerTimeout = 2 * time.Minute
	redisOpTimeout    = 2 * time.Second
)

// reasonClaimsChanged is the revocation reason that ends no session (auth.RevokeClaimsChanged): it
// sends no push (R50).
const reasonClaimsChanged = "claims_changed"

// FCM is the notify.fcm consumer (main spec §7.6, Appendix B §B.5.3). For each message it reads the
// recipients server-side (never from the message), sends one FCM HTTP v1 request per device token and
// logs one notification_deliveries row per token:
//
//   - task.assigned / task.reassigned / task.cancelled: the visible legacy pushes
//     {first_mile|line_haul}_task_{assigned|unassigned|cancelled} on channel task_assignments;
//   - task.updated / task.checked_in / task.plan_date_changed: the silent tasks_changed push to the
//     task's driver and helper, one per driver per 30 s (R21; a suppressed push logs deduplicated);
//   - user.sessions_revoked: the silent session_revoked push to the device of each revoked session
//     (device_tokens.install_id = sessions.install_id, R50, R83, R84); claims_changed sends none.
//
// Idempotency (at most once per token): a push is claimed in Redis (idem:fcm:{messageId}:{tokenId},
// 24 h) before it is sent and the claim keeps its outcome, so a redelivered or retried message never
// sends to a token twice. Only a send that certainly never reached FCM (a refused connection, 429, 5xx,
// the sender's misconfiguration) releases its claim for the retry; a send whose answer was lost after
// the request was written (reset, timeout, cancellation) may have been delivered and is recorded failed
// ("outcome unknown"), never resent, like a claim whose worker died before the answer. Every settled
// push is also kept in idem:fcm:{messageId}:log, so the pushes of an earlier attempt are logged even
// when the retry's plan no longer holds their device. The delivery rows, the deletion of UNREGISTERED
// tokens and the consumer_inbox claim commit together once every push of the message has an outcome.
// Redis unreachable means nothing is sent (the message retries).
type FCM struct {
	Pool    db.Beginner
	Sender  push.Sender // required when Enabled
	Redis   redis.UniversalClient
	Keys    cache.Keyspace
	Enabled bool // FCM_ENABLED: false acks every message without sending (local default)
	Log     zerolog.Logger
	Now     func() time.Time // nil: time.Now
}

// Registration binds the consumer to its queue.
func (f *FCM) Registration() mq.Registration {
	return mq.Registration{Queue: QueueFCM, Handler: f.Handle, Timeout: fcmHandlerTimeout}
}

// logger is the delivery's logger (the consumer puts message id, routing key and attempts on it), else
// Log.
func (f *FCM) logger(ctx context.Context) *zerolog.Logger {
	if l := zerolog.Ctx(ctx); l.GetLevel() != zerolog.Disabled {
		return l
	}
	return &f.Log
}

func (f *FCM) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// target is one push to one device.
type target struct {
	userID    uuid.UUID
	installID string
	token     string
	dedupe    *uuid.UUID // tasks_changed: the driver whose 30 s window applies
	push      Push
}

// result is a target with its outcome.
type result struct {
	target
	status string
	err    string
	at     time.Time
}

type planner func(ctx context.Context, q *notifydb.Queries, d *mq.Delivery) ([]target, error)

func (f *FCM) planner(key string) (planner, bool) {
	switch key {
	case RouteTaskAssigned, RouteTaskReassigned, RouteTaskCancelled, RouteTaskUpdated, RouteTaskCheckedIn,
		RouteTaskPlanDateChanged:
		return planTask, true
	case RouteSessionsRevoked:
		return planSessionsRevoked, true
	}
	return nil, false
}

// Handle implements mq.Handler.
func (f *FCM) Handle(ctx context.Context, d *mq.Delivery) error {
	if !f.Enabled {
		f.logger(ctx).Debug().Msg("FCM_ENABLED is false: nothing sent")
		return nil
	}
	if f.Sender == nil {
		return errors.New("notify.fcm: FCM is enabled without a sender")
	}
	plan, ok := f.planner(d.RoutingKey)
	if !ok {
		if issue, pending := pendingRoutes[d.RoutingKey]; pending {
			f.logger(ctx).Error().Str("lands_with", issue).Msg("notify.fcm: no recipients planner for this event yet; the message will dead-letter for a replay")
			return fmt.Errorf("notify.fcm: %s has no planner before %s", d.RoutingKey, issue)
		}
		return mq.Permanent(fmt.Errorf("notify.fcm: unexpected routing key %s", d.RoutingKey))
	}
	if d.MessageID == "" {
		return mq.Permanent(errors.New("notify.fcm: the message has no message id"))
	}
	var targets []target
	recorded := false
	err := db.WithSystem(ctx, f.Pool, d.TenantID, func(tx pgx.Tx) error {
		q := notifydb.New(tx)
		var err error
		if recorded, err = q.InboxClaimed(ctx, notifydb.InboxClaimedParams{Consumer: d.Queue, MessageID: d.MessageID}); err != nil || recorded {
			return err
		}
		targets, err = plan(ctx, q, d)
		return err
	})
	if err != nil || recorded {
		return err
	}
	var results []result
	if len(targets) > 0 {
		if results, err = f.deliver(ctx, d.MessageID, targets); err != nil {
			return err
		}
	}
	// A retry re-plans from the current rows: a push an earlier attempt sent to a device that has left
	// the plan since (task reassigned, install signed in again) is still logged.
	if results, err = f.withEarlierAttempts(ctx, d.MessageID, results); err != nil || len(results) == 0 {
		return err
	}
	return f.record(ctx, d, results)
}

// maxSendsPerMessage bounds the pushes of one message in flight, so a message with many devices takes
// turns with the other messages for the push client's slots instead of queueing all its sends first.
const maxSendsPerMessage = push.DefaultConcurrency

// deliver sends the targets concurrently, at most maxSendsPerMessage at a time (the push client also
// bounds the requests in flight per process), and returns the outcomes, or the first transient error
// once every target has been tried.
func (f *FCM) deliver(ctx context.Context, messageID string, targets []target) ([]result, error) {
	results := make([]result, len(targets))
	errs := make([]error, len(targets))
	dd := &dedupe{f: f, messageID: messageID, decided: map[uuid.UUID]dedupeDecision{}}
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(len(targets), maxSendsPerMessage) {
		wg.Go(func() {
			for i := range next {
				results[i], errs[i] = f.deliverOne(ctx, messageID, targets[i], dd)
			}
		})
	}
	for i := range targets {
		next <- i
	}
	close(next)
	wg.Wait()
	failed := 0
	var first error
	for _, err := range errs {
		if err != nil {
			failed++
			if first == nil {
				first = err
			}
		}
	}
	if failed > 0 {
		return nil, fmt.Errorf("notify.fcm: %d of %d pushes need a retry: %w", failed, len(targets), first)
	}
	return results, nil
}

// deliverOne claims, sends and settles one push. A nil error means res holds the push's outcome; an
// error means it must be retried with the message.
func (f *FCM) deliverOne(ctx context.Context, messageID string, t target, dd *dedupe) (result, error) {
	res := result{target: t}
	key := f.Keys.IdemFCM(messageID, TokenID(t.token))
	c, err := f.claim(ctx, key)
	if err != nil {
		return res, err
	}
	switch c.state {
	case claimDone:
		res.status, res.err, res.at = c.outcome.status, c.outcome.err, c.outcome.at
		return res, nil
	case claimInFlight:
		if f.now().Sub(c.since) < InFlightStale {
			return res, errors.New("another attempt of this message is sending to the device")
		}
		// The attempt that claimed it died before the outcome: the push may or may not have left, and
		// sending again could duplicate it, so it is recorded as failed and not sent.
		res.status, res.err, res.at = StatusFailed, "interrupted: the attempt that claimed this push did not finish", f.now()
		f.settle(ctx, messageID, key, res)
		return res, nil
	}
	if t.dedupe != nil {
		suppressed, err := dd.suppressed(ctx, *t.dedupe)
		if err != nil {
			f.release(ctx, key, c.marker)
			return res, err
		}
		if suppressed {
			res.status, res.at = StatusDeduplicated, f.now()
			f.settle(ctx, messageID, key, res)
			return res, nil
		}
	}
	err = f.Sender.Send(ctx, t.push.Message(t.token))
	res.at = f.now()
	switch {
	case err == nil:
		res.status = StatusSent
	case push.IsTokenInvalid(err):
		res.status, res.err = StatusTokenInvalid, err.Error()
	case push.MaybeDelivered(err):
		// The request reached FCM and its answer was lost: the push may be on the phone already, and
		// sending it again could show it twice. It keeps its claim and is never resent.
		res.status, res.err = StatusFailed, "outcome unknown: "+err.Error()
	case push.IsTransient(err):
		f.release(ctx, key, c.marker)
		return res, err
	default:
		res.status, res.err = StatusFailed, err.Error()
	}
	f.settle(ctx, messageID, key, res)
	return res, nil
}

// logEntry is one settled push in idem:fcm:{messageId}:log (field {tokenId}): what its
// notification_deliveries row needs, without the token.
type logEntry struct {
	UserID    uuid.UUID         `json:"userId"`
	InstallID string            `json:"installId"`
	Kind      string            `json:"kind"`
	Data      map[string]string `json:"data"`
	Status    string            `json:"status"`
	Error     string            `json:"error,omitempty"`
	At        int64             `json:"at"` // unix ms
}

// withEarlierAttempts adds to results the pushes earlier attempts of the message settled for devices
// the current plan no longer holds (their tokenId is not among results). Such a row has no token, so a
// token_invalid one does not delete the device row (the next push to it does).
func (f *FCM) withEarlierAttempts(ctx context.Context, messageID string, results []result) ([]result, error) {
	rctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	logged, err := f.Redis.HGetAll(rctx, f.Keys.IdemFCMLog(messageID)).Result()
	if err != nil {
		return nil, fmt.Errorf("idem:fcm log: %w", err)
	}
	if len(logged) == 0 {
		return results, nil
	}
	planned := make(map[string]bool, len(results))
	for _, r := range results {
		planned[TokenID(r.token)] = true
	}
	tokenIDs := make([]string, 0, len(logged))
	for id := range logged {
		if !planned[id] {
			tokenIDs = append(tokenIDs, id)
		}
	}
	slices.Sort(tokenIDs)
	for _, id := range tokenIDs {
		var e logEntry
		if err := json.Unmarshal([]byte(logged[id]), &e); err != nil || e.Kind == "" || e.Status == "" {
			f.logger(ctx).Warn().Str("token_id", id).Msg("notify.fcm: unreadable idem:fcm log entry skipped")
			continue
		}
		results = append(results, result{
			target: target{userID: e.UserID, installID: e.InstallID, push: Push{Kind: e.Kind, Data: e.Data}},
			status: e.Status, err: e.Error, at: time.UnixMilli(e.At),
		})
	}
	return results, nil
}

// record commits the outcomes: the consumer_inbox claim, one notification_deliveries row per push and
// the deletion of the tokens FCM rejected. A duplicate that recorded first makes this a no-op.
func (f *FCM) record(ctx context.Context, d *mq.Delivery, results []result) error {
	var eventID *int64
	if n, err := strconv.ParseInt(d.MessageID, 10, 64); err == nil {
		eventID = &n
	}
	counts := map[string]int{}
	processed, err := inbox.Run(ctx, f.Pool, d, func(tx pgx.Tx) error {
		q := notifydb.New(tx)
		for _, r := range results {
			payload, err := json.Marshal(r.push.Data)
			if err != nil {
				return mq.Permanent(err)
			}
			p := notifydb.InsertDeliveryParams{
				OutboxEventID: eventID, UserID: &r.userID, InstallID: &r.installID, Kind: r.push.Kind,
				Payload: payload, Status: r.status, CreatedAt: f.now(),
			}
			if r.err != "" {
				p.Error = &r.err
			}
			if r.status == StatusSent {
				at := r.at
				p.SentAt = &at
			}
			if err := q.InsertDelivery(ctx, p); err != nil {
				return err
			}
			if r.status == StatusTokenInvalid && r.token != "" {
				if _, err := q.DeleteInvalidToken(ctx, notifydb.DeleteInvalidTokenParams{
					UserID: r.userID, InstallID: r.installID, Token: r.token,
				}); err != nil {
					return err
				}
			}
			counts[r.push.Kind+":"+r.status]++
		}
		return nil
	})
	if err == nil && processed {
		f.logger(ctx).Info().Interface("pushes", counts).Msg("pushes recorded")
	}
	return err
}

// TokenID is the {tokenId} of idem:fcm:{messageId}:{tokenId}: the first 16 hex characters of
// sha256(token), as the ETL derives legacy install ids (Appendix A §A.3.1), so keys never hold a token.
func TokenID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// --- planners ---------------------------------------------------------------------------------------

// taskEvent is the part of a task.* payload notify.fcm reads: the task id (id, as in the thin SSE
// payload of Appendix B §B.4.1); on task.assigned and task.reassigned the driver this change assigned
// (driverId, required: null on task.reassigned means the change left the task without a driver) and on
// task.reassigned the one it replaced (previousDriverId, null or absent when there was none).
// Everything else is re-read from tasks.
type taskEvent struct {
	ID               uuid.UUID    `json:"id"`
	DriverID         optionalUUID `json:"driverId"`
	PreviousDriverID *uuid.UUID   `json:"previousDriverId"`
}

// optionalUUID tells a key that is absent (set false) from one that is null (set, id nil).
type optionalUUID struct {
	set bool
	id  *uuid.UUID
}

// UnmarshalJSON implements json.Unmarshaler; it runs only for a key that is present, null included.
func (o *optionalUUID) UnmarshalJSON(b []byte) error {
	o.set, o.id = true, nil
	if string(b) == "null" {
		return nil
	}
	var id uuid.UUID
	if err := json.Unmarshal(b, &id); err != nil {
		return err
	}
	o.id = &id
	return nil
}

// driverPush is a push for every device of one driver; build receives the driver's app-side id.
type driverPush struct {
	driverID uuid.UUID
	silent   bool // tasks_changed: subject to the 30 s window
	build    func(driverRef string) Push
}

func planTask(ctx context.Context, q *notifydb.Queries, d *mq.Delivery) ([]target, error) {
	var ev taskEvent
	if err := d.Decode(&ev); err != nil {
		return nil, err
	}
	if ev.ID == uuid.Nil {
		return nil, mq.Permanent(fmt.Errorf("notify.fcm: %s without id", d.RoutingKey))
	}
	// The driver this change assigned. An unassignment (task.reassigned, driverId null) must not be read
	// as "whoever holds the task now", so the key is required on both assignment events.
	var assigned *uuid.UUID
	switch d.RoutingKey {
	case RouteTaskAssigned, RouteTaskReassigned:
		if !ev.DriverID.set || (d.RoutingKey == RouteTaskAssigned && ev.DriverID.id == nil) {
			return nil, mq.Permanent(fmt.Errorf("notify.fcm: %s without driverId", d.RoutingKey))
		}
		assigned = ev.DriverID.id
	}
	t, err := q.GetTaskForPush(ctx, ev.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, mq.Permanent(fmt.Errorf("notify.fcm: task %s not found", ev.ID))
	}
	if err != nil {
		return nil, err
	}
	current := t.DriverID
	same := func(a, b *uuid.UUID) bool { return a != nil && b != nil && *a == *b }
	// The driver this change assigned still holds the task: the event may be read after a later change,
	// or re-planned by a retry after one, and then that change's own event tells the driver it holds.
	stillAssigned := t.Status != "cancelled" && same(assigned, current)
	var pushes []driverPush
	switch d.RoutingKey {
	case RouteTaskAssigned:
		if stillAssigned {
			pushes = append(pushes, driverPush{driverID: *current, build: func(ref string) Push {
				return TaskAssigned(t.TaskType, t.TaskRef, ref, t.SourceHubRaw, t.DestinationRaw, t.PlanDate, t.PlanTime)
			}})
		}
	case RouteTaskReassigned:
		// The previous driver hears it lost the task, unless the change was none or the task has come
		// back to that driver since.
		if prev := ev.PreviousDriverID; prev != nil && !same(prev, assigned) && !same(prev, current) {
			pushes = append(pushes, driverPush{driverID: *prev, build: func(ref string) Push {
				return TaskUnassigned(t.TaskType, t.TaskRef, ref)
			}})
		}
		if stillAssigned && !same(ev.PreviousDriverID, assigned) {
			pushes = append(pushes, driverPush{driverID: *current, build: func(ref string) Push {
				return TaskAssigned(t.TaskType, t.TaskRef, ref, t.SourceHubRaw, t.DestinationRaw, t.PlanDate, t.PlanTime)
			}})
		}
	case RouteTaskCancelled:
		if t.Status == "cancelled" && current != nil {
			pushes = append(pushes, driverPush{driverID: *current, build: func(ref string) Push {
				return TaskCancelled(t.TaskType, t.TaskRef, ref)
			}})
		}
	default: // task.updated, task.checked_in, task.plan_date_changed: the queues of driver and helper
		for _, id := range []*uuid.UUID{t.DriverID, t.HelperDriverID} {
			if id != nil {
				pushes = append(pushes, driverPush{driverID: *id, silent: true, build: func(string) Push { return TasksChanged() }})
			}
		}
	}
	return driverTargets(ctx, q, pushes)
}

// driverTargets expands driver pushes to one target per device of each driver.
func driverTargets(ctx context.Context, q *notifydb.Queries, pushes []driverPush) ([]target, error) {
	if len(pushes) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, 0, len(pushes))
	for _, p := range pushes {
		ids = append(ids, p.driverID)
	}
	rows, err := q.DriverDevices(ctx, notifydb.DriverDevicesParams{DriverIds: ids, PerDriver: MaxDevicesPerDriver})
	if err != nil {
		return nil, err
	}
	var out []target
	for _, p := range pushes {
		for _, r := range rows {
			if r.DriverID != p.driverID {
				continue
			}
			t := target{userID: r.UserID, installID: r.InstallID, token: r.Token, push: p.build(r.DriverRef)}
			if p.silent {
				id := p.driverID
				t.dedupe = &id
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// sessionsRevoked is the payload of user.sessions_revoked (Appendix C §C.4.7).
type sessionsRevoked struct {
	UserID     uuid.UUID   `json:"userId"`
	SessionIDs []uuid.UUID `json:"sessionIds"`
	Reason     string      `json:"reason"`
}

func planSessionsRevoked(ctx context.Context, q *notifydb.Queries, d *mq.Delivery) ([]target, error) {
	var ev sessionsRevoked
	if err := d.Decode(&ev); err != nil {
		return nil, err
	}
	if ev.UserID == uuid.Nil || ev.Reason == "" {
		return nil, mq.Permanent(errors.New("notify.fcm: user.sessions_revoked without userId or reason"))
	}
	// claims_changed ends no session: the next request fails the ver check and the app refreshes.
	if ev.Reason == reasonClaimsChanged || len(ev.SessionIDs) == 0 {
		return nil, nil
	}
	rows, err := q.RevokedSessionDevices(ctx, notifydb.RevokedSessionDevicesParams{UserID: ev.UserID, SessionIds: ev.SessionIDs})
	if err != nil {
		return nil, err
	}
	out := make([]target, 0, len(rows))
	for _, r := range rows {
		out = append(out, target{userID: ev.UserID, installID: r.InstallID, token: r.Token,
			push: SessionRevoked(ev.Reason, r.SessionID.String())})
	}
	return out, nil
}

// --- Redis: idem:fcm claims and the tasks_changed window ------------------------------------------

type claimState int

const (
	claimNew      claimState = iota // this attempt holds the claim and sends
	claimDone                       // an earlier attempt settled the push: reuse its outcome
	claimInFlight                   // another attempt holds the claim
)

type claimResult struct {
	state   claimState
	marker  string    // the pending value this attempt wrote (claimNew)
	since   time.Time // when the holder claimed it (claimInFlight)
	outcome outcome   // claimDone
}

type outcome struct {
	status string
	err    string
	at     time.Time
}

// Claim values: "pending|{unix ms}|{nonce}" while a send is in flight, then
// "{status}|{unix ms}|{error}" with the outcome.
const pendingPrefix = "pending"

func (f *FCM) claim(ctx context.Context, key string) (claimResult, error) {
	rctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	marker := pendingPrefix + "|" + strconv.FormatInt(f.now().UnixMilli(), 10) + "|" + uuid.NewString()
	for range 2 {
		ok, err := f.Redis.SetNX(rctx, key, marker, PushIdemTTL).Result()
		if err != nil {
			return claimResult{}, fmt.Errorf("idem:fcm claim: %w", err)
		}
		if ok {
			return claimResult{state: claimNew, marker: marker}, nil
		}
		v, err := f.Redis.Get(rctx, key).Result()
		if errors.Is(err, redis.Nil) {
			continue // expired between the two calls
		}
		if err != nil {
			return claimResult{}, fmt.Errorf("idem:fcm read: %w", err)
		}
		return parseClaim(v), nil
	}
	return claimResult{}, errors.New("idem:fcm claim: the key kept changing")
}

// parseClaim reads a claim value; a value this code never writes counts as a finished failure, so it
// is not sent again.
func parseClaim(v string) claimResult {
	status, rest, _ := strings.Cut(v, "|")
	ms, rest, _ := strings.Cut(rest, "|")
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return claimResult{state: claimDone, outcome: outcome{status: StatusFailed, err: "unreadable idem:fcm value"}}
	}
	at := time.UnixMilli(n)
	if status == pendingPrefix {
		return claimResult{state: claimInFlight, since: at}
	}
	switch status {
	case StatusSent, StatusFailed, StatusTokenInvalid, StatusDeduplicated:
		return claimResult{state: claimDone, outcome: outcome{status: status, err: rest, at: at}}
	}
	return claimResult{state: claimDone, outcome: outcome{status: StatusFailed, err: "unreadable idem:fcm value"}}
}

// settle stores the outcome on the claim (24 h from now) and in the message's log
// (idem:fcm:{messageId}:log, field {tokenId}, 24 h from the last outcome). A failure is logged: the
// push is done, and the delivery row still records it while the device stays in the plan; only a
// later attempt of the same message would then find the claim pending and, once stale, record it as
// interrupted instead of sending again.
func (f *FCM) settle(ctx context.Context, messageID, key string, r result) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), redisOpTimeout)
	defer cancel()
	ms := r.at.UnixMilli()
	entry, err := json.Marshal(logEntry{UserID: r.userID, InstallID: r.installID, Kind: r.push.Kind, Data: r.push.Data,
		Status: r.status, Error: r.err, At: ms})
	if err != nil {
		entry = nil
	}
	logKey := f.Keys.IdemFCMLog(messageID)
	_, err = f.Redis.Pipelined(rctx, func(p redis.Pipeliner) error {
		p.Set(rctx, key, r.status+"|"+strconv.FormatInt(ms, 10)+"|"+r.err, PushIdemTTL)
		if entry != nil {
			p.HSet(rctx, logKey, TokenID(r.token), entry)
			p.Expire(rctx, logKey, PushIdemTTL)
		}
		return nil
	})
	if err != nil {
		f.logger(ctx).Warn().Err(err).Str("status", r.status).Msg("notify.fcm: could not store a push outcome in idem:fcm")
	}
}

// releaseScript deletes a claim only while it still holds this attempt's marker.
var releaseScript = redis.NewScript(`if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0`)

// release drops this attempt's claim after a transient failure, so the retry may send. A failure is
// logged: the claim then turns stale and the retry records the push as interrupted.
func (f *FCM) release(ctx context.Context, key, marker string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), redisOpTimeout)
	defer cancel()
	if err := releaseScript.Run(rctx, f.Redis, []string{key}, marker).Err(); err != nil && !errors.Is(err, redis.Nil) {
		f.logger(ctx).Warn().Err(err).Msg("notify.fcm: could not release an idem:fcm claim")
	}
}

type dedupeDecision struct {
	suppressed bool
	err        error
}

// dedupe decides the tasks_changed window once per driver and message attempt:
// SET idem:push:tasks_changed:{driverId} {messageId} NX EX 30. The key holds the message that opened
// the window, so a retry of that message still sends, and any other message inside the 30 s is
// suppressed (R21).
type dedupe struct {
	f         *FCM
	messageID string
	mu        sync.Mutex
	decided   map[uuid.UUID]dedupeDecision
}

func (d *dedupe) suppressed(ctx context.Context, driverID uuid.UUID) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if v, ok := d.decided[driverID]; ok {
		return v.suppressed, v.err
	}
	v := d.decide(ctx, driverID)
	if v.err == nil {
		d.decided[driverID] = v
	}
	return v.suppressed, v.err
}

func (d *dedupe) decide(ctx context.Context, driverID uuid.UUID) dedupeDecision {
	rctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	key := d.f.Keys.IdemTasksChangedPush(driverID.String())
	for range 2 {
		ok, err := d.f.Redis.SetNX(rctx, key, d.messageID, TasksChangedWindow).Result()
		if err != nil {
			return dedupeDecision{err: fmt.Errorf("idem:push window: %w", err)}
		}
		if ok {
			return dedupeDecision{}
		}
		holder, err := d.f.Redis.Get(rctx, key).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return dedupeDecision{err: fmt.Errorf("idem:push window: %w", err)}
		}
		return dedupeDecision{suppressed: holder != d.messageID}
	}
	return dedupeDecision{err: errors.New("idem:push window: the key kept changing")}
}
