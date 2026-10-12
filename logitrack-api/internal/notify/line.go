package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify/notifydb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/inbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/line"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// QueueLine is the work queue of the LINE consumer (Appendix B §B.5.3).
const QueueLine = "notify.line"

// Routing keys bound to notify.line (Appendix B §B.5.3); task.checked_in is RouteTaskCheckedIn.
const (
	RouteTripDelivered    = "trip.delivered"
	RouteStandbyCompleted = "standby.completed"
	RouteLineForce        = "job." + jobs.TypeNotifyLineForce
)

// The three cards, named as the legacy callable's events (sendCustomerLineNotification {event}).
const (
	LineCheckin   = "checkin"
	LineDelivered = "delivered"
	LineStandby   = "standby"
)

// LineForceParams are the params of a notify.line.force job: a staff request (POST /v1/{tasks|trips|standby}/{id}/
// notify-line, force optional) or a driver's shim request (POST /v1/mobile/{tasks|trips|standby}/{id}/line-notify,
// never forced). ID is the PostgreSQL id when the route read the record; Ref the id the caller named (a Firestore
// document id, a trip number) when the record was not in PostgreSQL yet (the mirror loads it later: the job waits for
// it up to LineMirrorWait). DriverID makes the job a driver's: only a record of that driver is notified.
type LineForceParams struct {
	Card     string     `json:"card"`
	ID       *uuid.UUID `json:"id,omitempty"`
	Ref      string     `json:"ref,omitempty"`
	Force    bool       `json:"force"`
	DriverID *uuid.UUID `json:"driverId,omitempty"`
}

// Validate reports params a job could never act on.
func (p LineForceParams) Validate() error {
	switch {
	case p.Card != LineCheckin && p.Card != LineDelivered && p.Card != LineStandby:
		return fmt.Errorf("notify.line: unknown card %q", p.Card)
	case p.ID == nil && strings.TrimSpace(p.Ref) == "":
		return errors.New("notify.line: the job names no record")
	case p.Force && p.DriverID != nil:
		return errors.New("notify.line: a driver's request is never forced")
	}
	return nil
}

// Timing and outcomes of the LINE consumer.
const (
	// LineIdemTTL is the life of idem:line:{recordId}:{event}, LINE's retry-key window (Appendix B §B.6.2).
	LineIdemTTL = 24 * time.Hour
	// LineMirrorWait bounds how long a request waits for a record the FS->PG mirror has not loaded (a shim request on a
	// Firestore id, the notify event of a Go write-back): retried transiently until then, settled skipped after.
	LineMirrorWait = 15 * time.Minute

	lineHandlerTimeout = time.Minute
)

// Reasons of a card that is not sent (the legacy {skipped, reason} texts where one exists).
const (
	ReasonAlreadyNotified = "already notified"
	ReasonNoLineGroup     = "no lineGroupId configured"
	ReasonLineDisabled    = "LINE_NOTIFY_ENABLED is false"
	ReasonRecordNotFound  = "record not found"
	ReasonNotTaskOwner    = "not_task_owner"
	ReasonSentByOther     = "sent by another message within 24 h"
)

// LINE is the notify.line consumer (main spec §7.6, Appendix B §B.5.3): the port of the callable
// sendCustomerLineNotification (fn:lineNotify.ts). It sends the check-in card for task.checked_in, the delivered card
// for trip.delivered and the standby card for standby.completed, and the card a job.notify.line.force names (a staff
// force or resend, a driver's shim request), to the LINE group of the record's party: a task's source party, else its
// destination party, else the trip's billing party; a standby record's customer party only (fn:lineNotify.ts:134-159,
// 231-236). A record without a group is skipped. Values are read from PostgreSQL in the consumer's system transaction.
//
// Once per card (0 duplicate sends): the record's flag (line_checkin_notified_at, line_delivered_notified_at,
// line_notified_at) is checked first unless the job forces, and set with the send's outcome; the senders of one card
// of one record are serialised by an advisory lock; SET idem:line:{recordId}:{event} NX (24 h) keeps a second message
// from sending a card another message is sending or sent without the flag (a commit lost after LINE accepted); and
// X-Line-Retry-Key is a UUIDv5 of (card, record, the forcing job), so a retry whose first answer was lost is LINE's 409
// replay, never a second card. Only a forced job sends a card the flag says was sent, and it mints a new evidence token
// when the old one was revoked (R47).
//
// Evidence: a delivered or standby card with photos carries "ดูรูปหลักฐาน (N)" linking {EVIDENCE_BASE_URL}/{token}; a
// missing token is minted (storage.EvidenceTokenForSend) and committed before the push, in a transaction of its own,
// so the link a group received always resolves. N is what the gallery shows (committed photos of the trip, its stops
// and its incidents; of the standby record).
//
// Coexistence: the events of the FS->PG mirror (origin=etl.sync) are acked without a card, since the Firestore-side
// writer (the legacy callable, or its shim, which forwards here as a job) owns the notification; the notify event of a
// Go write-back (origin=writeback) is that writer's, and waits until the mirror applied its write. Errors: 429, 5xx and
// a request without an answer retry; other 4xx are recorded skipped with LINE's message.
type LINE struct {
	Pool   db.Beginner
	Client line.Pusher // required when Enabled
	Redis  redis.UniversalClient
	Keys   cache.Keyspace
	// Locks releases lock:job:notify.line.force:{scope} when a job ends (nil: the lock lives jobs.LockTTL).
	Locks jobs.Locker
	// Enabled is LINE_NOTIFY_ENABLED: false acks every event without sending and ends every job skipped.
	Enabled bool
	// EvidenceBaseURL is EVIDENCE_BASE_URL: the gallery the cards link (EvidenceLink).
	EvidenceBaseURL string
	Log             zerolog.Logger
	Now             func() time.Time // nil: time.Now
}

// Registration binds the consumer to its queue.
func (l *LINE) Registration() mq.Registration {
	return mq.Registration{Queue: QueueLine, Handler: l.Handle, Timeout: lineHandlerTimeout}
}

func (l *LINE) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *LINE) logger(ctx context.Context) *zerolog.Logger {
	if lg := zerolog.Ctx(ctx); lg.GetLevel() != zerolog.Disabled {
		return lg
	}
	return &l.Log
}

// lineRequest is what one message asks for.
type lineRequest struct {
	card     string
	id       *uuid.UUID
	ref      string
	forced   bool
	driverID *uuid.UUID
	job      *jobs.Command
	// waitFrom: a record that is not found (or, for a driver's request, not ready) is waited for until waitFrom +
	// LineMirrorWait; zero for an event of a PostgreSQL write, whose row committed with it.
	waitFrom  time.Time
	writeBack *writeBackRef
}

// lineEvent is the part of a task.checked_in, trip.delivered or standby.completed payload the consumer reads: the
// record id, and for a Go write-back the Firestore document and the update time of its commit.
type lineEvent struct {
	ID    uuid.UUID  `json:"id"`
	DocID string     `json:"legacyDocId"`
	At    *time.Time `json:"writeUpdateTime"`
}

func (l *LINE) decode(d *mq.Delivery) (lineRequest, error) {
	var card string
	switch d.RoutingKey {
	case RouteTaskCheckedIn:
		card = LineCheckin
	case RouteTripDelivered:
		card = LineDelivered
	case RouteStandbyCompleted:
		card = LineStandby
	case RouteLineForce:
		cmd, err := jobs.DecodeCommand(d)
		if err != nil {
			return lineRequest{}, err
		}
		req := lineRequest{job: &cmd}
		var p LineForceParams
		if err := json.Unmarshal(cmd.Params, &p); err != nil {
			return req, mq.Permanent(fmt.Errorf("notify.line: job params: %w", err))
		}
		if err := p.Validate(); err != nil {
			return req, mq.Permanent(err)
		}
		req.card, req.id, req.ref, req.forced, req.driverID = p.Card, p.ID, strings.TrimSpace(p.Ref), p.Force, p.DriverID
		return req, nil
	default:
		return lineRequest{}, mq.Permanent(fmt.Errorf("notify.line: unexpected routing key %s", d.RoutingKey))
	}
	var ev lineEvent
	if err := d.Decode(&ev); err != nil {
		return lineRequest{}, err
	}
	req := lineRequest{card: card}
	if ev.ID != uuid.Nil {
		id := ev.ID
		req.id = &id
	}
	req.ref = strings.TrimSpace(ev.DocID)
	if req.id == nil && req.ref == "" {
		return lineRequest{}, mq.Permanent(errors.New("notify.line: the event names no record"))
	}
	if FromWriteBack(d) && ev.At != nil {
		req.writeBack = &writeBackRef{DocID: req.ref, At: ev.At}
		req.waitFrom = *ev.At
	}
	return req, nil
}

// Handle implements mq.Handler.
func (l *LINE) Handle(ctx context.Context, d *mq.Delivery) error {
	if FromMirror(d) {
		// The FS->PG mirror re-tells what a Firestore writer did (T29): that writer owns the card (the legacy callable,
		// or its shim, which forwards here as a job), so a second one is never sent.
		l.logger(ctx).Debug().Msg("notify.line: an event of the FS->PG mirror: no card")
		return nil
	}
	req, err := l.decode(d)
	if err != nil {
		if req.job != nil && mq.IsPermanent(err) {
			return l.finishJob(ctx, d, req, lineOutcome{status: StatusSkipped, reason: err.Error(), fail: true})
		}
		return err
	}
	if !l.Enabled {
		if req.job != nil {
			return l.finishJob(ctx, d, req, lineOutcome{status: StatusSkipped, reason: ReasonLineDisabled})
		}
		l.logger(ctx).Debug().Msg("LINE_NOTIFY_ENABLED is false: nothing sent")
		return nil
	}
	if l.Client == nil {
		return errors.New("notify.line: LINE is enabled without a client")
	}
	if d.MessageID == "" {
		return mq.Permanent(errors.New("notify.line: the message has no message id"))
	}
	if req.writeBack != nil {
		if err := l.awaitMirror(ctx, req); err != nil {
			return err
		}
	}
	err = l.run(ctx, d, req)
	if req.job != nil && mq.IsPermanent(err) {
		return l.finishJob(ctx, d, req, lineOutcome{status: StatusSkipped, reason: err.Error(), fail: true})
	}
	return err
}

// StatusSkipped is a card that is not sent and will not be for this message (notification_deliveries.status, 0023).
const StatusSkipped = "skipped"

// lineOutcome is how a message settled its card.
type lineOutcome struct {
	status string // sent | deduplicated | skipped
	reason string
	fail   bool // a job ends failed (record missing, another driver's, LINE refused); otherwise succeeded
	card   *lineCard
	result line.Result
}

// errMint asks run to mint the evidence token of the record in a transaction of its own before the send.
type errMint struct{ id uuid.UUID }

func (e *errMint) Error() string { return "notify.line: the evidence token is minted first" }

// errNotYet is a record the mirror has not loaded (or, for a driver's request, not brought to the card's state) yet.
var errNotYet = errors.New("notify.line: the record is not mirrored yet")

func (l *LINE) run(ctx context.Context, d *mq.Delivery, req lineRequest) error {
	for range 3 {
		out, err := l.attempt(ctx, d, req)
		var mint *errMint
		if errors.As(err, &mint) {
			if err := l.mintToken(ctx, d, req, mint.id); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if out != nil {
			l.logOutcome(ctx, req, *out)
			if req.job != nil {
				l.releaseLock(ctx, req)
			}
		}
		return nil
	}
	return errors.New("notify.line: the evidence token changed while the card was built")
}

// attempt runs one side-effect transaction: the inbox claim, the job start, the record under the card's lock, the
// checks, the idem:line claim, the push, the flag, the delivery row and the job's end. A nil outcome is a message
// another delivery already processed.
func (l *LINE) attempt(ctx context.Context, d *mq.Delivery, req lineRequest) (*lineOutcome, error) {
	var out *lineOutcome
	_, err := inbox.Run(ctx, l.Pool, d, func(tx pgx.Tx) error {
		q := notifydb.New(tx)
		waitFrom := req.waitFrom
		if req.job != nil {
			j, err := jobs.Start(ctx, tx, req.job.JobID)
			if errors.Is(err, jobs.ErrNotActive) {
				return nil // a duplicate of a command whose job has ended; the inbox claim commits
			}
			if err != nil {
				return err
			}
			// A record the route did not find, and every driver's request (the 3.x app calls right after its Firestore
			// write), may still be on its way through the mirror; a staff request named a row that exists.
			if req.id == nil || req.driverID != nil {
				waitFrom = j.CreatedAt
			}
		}
		settle := func(o lineOutcome) error {
			out = &o
			return l.settle(ctx, tx, d, req, o)
		}
		id, found, err := l.resolve(ctx, q, req)
		if err != nil {
			return err
		}
		if !found {
			if !waitFrom.IsZero() && l.now().Before(waitFrom.Add(LineMirrorWait)) {
				return errNotYet
			}
			return settle(lineOutcome{status: StatusSkipped, reason: ReasonRecordNotFound, fail: true})
		}
		if err := q.LineLock(ctx, notifydb.LineLockParams{Card: req.card, RecordID: id.String()}); err != nil {
			return err
		}
		c, found, err := l.load(ctx, q, req.card, id)
		if err != nil {
			return err
		}
		if !found {
			return settle(lineOutcome{status: StatusSkipped, reason: ReasonRecordNotFound, fail: true})
		}
		if req.driverID != nil {
			if !c.ownedBy(*req.driverID) {
				return settle(lineOutcome{status: StatusSkipped, reason: ReasonNotTaskOwner, fail: true, card: &c})
			}
			if !c.ready && !waitFrom.IsZero() && l.now().Before(waitFrom.Add(LineMirrorWait)) {
				return errNotYet
			}
		}
		if !req.forced && c.notifiedAt != nil {
			return settle(lineOutcome{status: StatusDeduplicated, reason: ReasonAlreadyNotified, card: &c})
		}
		if c.group == "" {
			return settle(lineOutcome{status: StatusSkipped, reason: ReasonNoLineGroup, card: &c})
		}
		link := ""
		if c.evidence {
			token, mint, err := storage.EvidenceTokenForSend(c.token, c.revokedAt, c.photos, req.forced, l.now())
			if err != nil {
				return err
			}
			if mint {
				return &errMint{id: id}
			}
			if token != "" {
				link = storage.EvidenceLink(l.EvidenceBaseURL, token)
			}
		}
		event := req.card
		if req.forced {
			event += ":force:" + req.job.JobID.String()
		}
		idemKey := l.Keys.IdemLine(id.String(), event)
		held, err := l.claimIdem(ctx, idemKey, d.MessageID)
		if err != nil {
			return err
		}
		if !held {
			return settle(lineOutcome{status: StatusDeduplicated, reason: ReasonSentByOther, card: &c})
		}
		res, err := l.Client.Push(ctx, lineRetryKey(req.card, id, req), LinePushBody(c.group, c.message(link)))
		if err != nil {
			if line.IsTransient(err) {
				return err // rolled back; the idem claim stays this message's, and the retry key makes its retry safe
			}
			l.releaseIdem(ctx, idemKey, d.MessageID)
			return settle(lineOutcome{status: StatusSkipped, reason: err.Error(), fail: true, card: &c})
		}
		if err := l.markNotified(ctx, q, req.card, id); err != nil {
			return err
		}
		return settle(lineOutcome{status: StatusSent, card: &c, result: res})
	})
	return out, err
}

// resolve finds the record a request names: its PostgreSQL id, else the Firestore document id (trips also by number).
func (l *LINE) resolve(ctx context.Context, q *notifydb.Queries, req lineRequest) (uuid.UUID, bool, error) {
	var ids []uuid.UUID
	if req.id != nil {
		ids = append(ids, *req.id)
	}
	for _, id := range ids {
		var err error
		switch req.card {
		case LineCheckin:
			_, err = q.LineTask(ctx, id)
		case LineDelivered:
			_, err = q.LineTrip(ctx, id)
		default:
			_, err = q.LineStandby(ctx, id)
		}
		if err == nil {
			return id, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
	}
	ref := req.ref
	if ref == "" && req.id != nil {
		ref = req.id.String() // a write-back mints the Firestore document id as a uuid (R25)
	}
	if ref == "" {
		return uuid.Nil, false, nil
	}
	var id uuid.UUID
	var err error
	switch req.card {
	case LineCheckin:
		id, err = q.LineTaskIDByLegacy(ctx, ref)
	case LineDelivered:
		id, err = q.LineTripIDByRef(ctx, ref)
	default:
		id, err = q.LineStandbyIDByLegacy(ctx, ref)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

func (l *LINE) markNotified(ctx context.Context, q *notifydb.Queries, card string, id uuid.UUID) error {
	at := l.now()
	switch card {
	case LineCheckin:
		return q.MarkTaskLineNotified(ctx, notifydb.MarkTaskLineNotifiedParams{At: at, ID: id})
	case LineDelivered:
		return q.MarkTripLineNotified(ctx, notifydb.MarkTripLineNotifiedParams{At: at, ID: id})
	default:
		return q.MarkStandbyLineNotified(ctx, notifydb.MarkStandbyLineNotifiedParams{At: at, ID: id})
	}
}

// mintToken stores a new evidence token for the record in its own committed transaction, so the link the card carries
// resolves whatever happens to the send (a retry of the send reuses it).
func (l *LINE) mintToken(ctx context.Context, d *mq.Delivery, req lineRequest, id uuid.UUID) error {
	return db.WithSystem(ctx, l.Pool, d.TenantID, func(tx pgx.Tx) error {
		q := notifydb.New(tx)
		if err := q.LineLock(ctx, notifydb.LineLockParams{Card: req.card, RecordID: id.String()}); err != nil {
			return err
		}
		c, found, err := l.load(ctx, q, req.card, id)
		if err != nil || !found || !c.evidence {
			return err
		}
		token, mint, err := storage.EvidenceTokenForSend(c.token, c.revokedAt, c.photos, req.forced, l.now())
		if err != nil || !mint {
			return err
		}
		kind := storage.EvidenceTrip
		if req.card == LineStandby {
			kind = storage.EvidenceStandby
		}
		return storage.SetEvidenceToken(ctx, tx, kind, id, token)
	})
}

// claimIdem takes idem:line for this message (SET NX, 24 h). It reports false when another message holds it.
func (l *LINE) claimIdem(ctx context.Context, key, messageID string) (bool, error) {
	rctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	ok, err := l.Redis.SetNX(rctx, key, messageID, LineIdemTTL).Result()
	if err != nil {
		return false, fmt.Errorf("notify.line: idem:line: %w", err)
	}
	if ok {
		return true, nil
	}
	cur, err := l.Redis.Get(rctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return l.claimIdem(ctx, key, messageID) // expired between the two calls
	}
	if err != nil {
		return false, fmt.Errorf("notify.line: idem:line: %w", err)
	}
	return cur == messageID, nil
}

var releaseIdemScript = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) end return 0`)

// releaseIdem frees idem:line after LINE refused the card for good, so a later message may send it.
func (l *LINE) releaseIdem(ctx context.Context, key, messageID string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), redisOpTimeout)
	defer cancel()
	if err := releaseIdemScript.Run(rctx, l.Redis, []string{key}, messageID).Err(); err != nil {
		l.logger(ctx).Warn().Err(err).Msg("notify.line: idem:line not released; it expires in 24 h")
	}
}

// lineRetryNamespace is the UUIDv5 namespace of X-Line-Retry-Key.
var lineRetryNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://logitrack/notify.line/retry-key"))

// lineRetryKey is X-Line-Retry-Key: UUIDv5 of (card, record, the forcing job). Every automatic or driver request for a
// card of a record shares one key, so LINE refuses (409) a second copy within its 24 h window; a forced job has its own.
func lineRetryKey(card string, id uuid.UUID, req lineRequest) string {
	nonce := ""
	if req.forced {
		nonce = req.job.JobID.String()
	}
	return uuid.NewSHA1(lineRetryNamespace, []byte(card+":"+id.String()+":"+nonce)).String()
}

// settle records the outcome in the side-effect transaction: one notification_deliveries row and, for a job, its end.
func (l *LINE) settle(ctx context.Context, tx pgx.Tx, d *mq.Delivery, req lineRequest, o lineOutcome) error {
	var eventID *int64
	if n, err := strconv.ParseInt(d.MessageID, 10, 64); err == nil {
		eventID = &n
	}
	payload := map[string]any{"card": req.card, "forced": req.forced}
	if o.card != nil {
		payload["entity"], payload["id"], payload["photoCount"] = o.card.entity(), o.card.recordID, o.card.photos
	} else if req.id != nil {
		payload["id"] = *req.id
	}
	if req.ref != "" && o.card == nil {
		payload["ref"] = req.ref
	}
	if req.job != nil {
		payload["jobId"] = req.job.JobID
	}
	if o.result.Replayed {
		payload["replayed"] = true
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	at := l.now()
	p := notifydb.InsertDeliveryParams{OutboxEventID: eventID, Kind: "line_" + req.card, Payload: b, Status: o.status, CreatedAt: at}
	if o.reason != "" {
		p.Error = &o.reason
	}
	if o.status == StatusSent {
		p.SentAt = &at
	}
	if err := notifydb.New(tx).InsertDelivery(ctx, p); err != nil {
		return err
	}
	if req.job == nil {
		return nil
	}
	result := map[string]any{"sent": o.status == StatusSent}
	if o.status != StatusSent {
		result["skipped"], result["reason"] = true, o.reason
	}
	if o.card != nil {
		result["photoCount"] = o.card.photos
	}
	var ferr error
	if o.fail {
		_, ferr = jobs.Fail(ctx, tx, req.job.JobID, o.reason, result)
	} else {
		_, ferr = jobs.Succeed(ctx, tx, req.job.JobID, result)
	}
	return ferr
}

// finishJob ends a job without a card (LINE off, unusable params) with the inbox claim, and releases its lock.
func (l *LINE) finishJob(ctx context.Context, d *mq.Delivery, req lineRequest, o lineOutcome) error {
	if req.job == nil {
		return nil
	}
	if req.card == "" {
		req.card = LineDelivered // unusable params: the row needs a kind; the payload names the job
	}
	processed, err := inbox.Run(ctx, l.Pool, d, func(tx pgx.Tx) error {
		if _, err := jobs.Start(ctx, tx, req.job.JobID); errors.Is(err, jobs.ErrNotActive) {
			return nil
		} else if err != nil {
			return err
		}
		return l.settle(ctx, tx, d, req, o)
	})
	if err != nil {
		return err
	}
	if processed {
		l.logOutcome(ctx, req, o)
	}
	l.releaseLock(ctx, req)
	return nil
}

func (l *LINE) releaseLock(ctx context.Context, req lineRequest) {
	if l.Locks == nil || req.job == nil || req.job.Scope == "" {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), redisOpTimeout)
	defer cancel()
	if err := l.Locks.Release(rctx, l.Locks.JobKey(req.job.Type, req.job.Scope), req.job.JobID.String()); err != nil {
		l.logger(ctx).Warn().Err(err).Msg("notify.line: the job lock was not released; it expires after jobs.LockTTL")
	}
}

func (l *LINE) logOutcome(ctx context.Context, req lineRequest, o lineOutcome) {
	ev := l.logger(ctx).Info()
	if o.status != StatusSent && o.fail {
		ev = l.logger(ctx).Warn()
	}
	ev = ev.Str("card", req.card).Str("status", o.status).Bool("forced", req.forced)
	if o.card != nil {
		ev = ev.Str("record_id", o.card.recordID.String())
	}
	if o.reason != "" {
		ev = ev.Str("reason", o.reason)
	}
	ev.Msg("LINE card settled")
}

// lineCollections are the Firestore collections of the records (cache:mirror_ack:{collection}:{docId}).
var lineCollections = map[string]string{LineCheckin: legacyTasks, LineDelivered: "trip_records", LineStandby: "standby_records"}

// awaitMirror holds the card of a Go write-back until the FS->PG mirror applied that write (the rule notify.fcm keeps
// for its pushes): before that the row may not exist yet, or show the record as it was before the write.
func (l *LINE) awaitMirror(ctx context.Context, req lineRequest) error {
	ref := req.writeBack
	if ref == nil || ref.DocID == "" || ref.At == nil || l.now().Sub(*ref.At) >= cache.MirrorAckTTL {
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	got, ok, err := cache.ReadMirrorAck(rctx, l.Redis, l.Keys, lineCollections[req.card], ref.DocID)
	if err != nil {
		return fmt.Errorf("notify.line: read the mirror acknowledgement: %w", err)
	}
	if ok && !got.Before(ref.At.Truncate(time.Microsecond)) {
		return nil
	}
	return fmt.Errorf("notify.line: the document %s written at %s is not mirrored yet", ref.DocID,
		ref.At.UTC().Format(time.RFC3339Nano))
}
