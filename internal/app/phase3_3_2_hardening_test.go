package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/mailer"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

// barrierMailer blocks inside Send until released by the test or ctx cancels,
// letting tests hold an in-flight external mail call cleanly with zero open PG tx.
type barrierMailer struct {
	mu          sync.Mutex
	sent        []mailer.Message
	started     chan mailer.Message
	release     chan error
	cancelled   chan error
	onCtxCancel func(error) error
	ignoreCtx   bool
}

func newBarrierMailer() *barrierMailer {
	return &barrierMailer{
		started:   make(chan mailer.Message, 8),
		release:   make(chan error, 8),
		cancelled: make(chan error, 8),
	}
}

func (m *barrierMailer) Send(ctx context.Context, msg mailer.Message) error {
	m.started <- msg
	if m.ignoreCtx {
		err := <-m.release
		if err == nil {
			m.mu.Lock()
			m.sent = append(m.sent, msg)
			m.mu.Unlock()
		}
		return err
	}
	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	select {
	case err := <-m.release:
		if err == nil {
			m.mu.Lock()
			m.sent = append(m.sent, msg)
			m.mu.Unlock()
		}
		return err
	case <-done:
		m.cancelled <- ctx.Err()
		if m.onCtxCancel != nil {
			return m.onCtxCancel(ctx.Err())
		}
		return mailer.TerminatedBeforeSend(ctx.Err())
	}
}

func (m *barrierMailer) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func TestPhase332Hardening_OutboxDispatchAndAnonymisationBarrier(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-hard-outbox")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	ownerActor := service.Actor{
		UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role,
		IPAddress: "127.0.0.1", UserAgent: "hardening-test",
	}

	bMailer := newBarrierMailer()
	svc.SetEmailSender(bMailer)

	createGuestWithQueuedEmail := func(tag string) (domain.Guest, uuid.UUID) {
		g, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
			PropertyID: prop.ID, FirstName: "Carys-" + tag, LastName: "Williams",
			Email: fmt.Sprintf("barrier.%s@example.test", tag),
		})
		if err != nil {
			t.Fatalf("create guest %s: %v", tag, err)
		}
		comm, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
			PropertyID: prop.ID,
			GuestID:    g.ID,
			Recipient:  g.Email,
			Subject:    fmt.Sprintf("Booking Ref %s for %s", tag, g.Email),
			BodyText:   fmt.Sprintf("Confidential itinerary for %s", g.Email),
		})
		if err != nil || comm.EmailOutboxID == nil {
			t.Fatalf("send guest email %s: comm=%+v err=%v", tag, comm, err)
		}
		return g, *comm.EmailOutboxID
	}

	// 1. Pre-transport abort: claimed into PROCESSING, then anonymised BEFORE BeginOutboxDispatch -> 0 sends, CANCELLED.
	gPre, outPre := createGuestWithQueuedEmail("pre")
	claimedPre, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(claimedPre) != 1 || claimedPre[0].ID != outPre {
		t.Fatalf("claim pre: %+v err=%v", claimedPre, err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gPre.ID, "Pre-transport erasure"); err != nil {
		t.Fatalf("pre-transport anonymize failed: %v", err)
	}
	allowedPre, err := store.BeginOutboxDispatch(ctx, outPre, claimedPre[0].WorkerToken, "worker-pre", clk.Now().UTC())
	if err != nil || allowedPre {
		t.Fatalf("expected BeginOutboxDispatch=false after pre-transport erasure, got allowed=%v err=%v", allowedPre, err)
	}
	var stPre, recPre, subjPre string
	_ = store.Pool().QueryRow(ctx, `SELECT status, recipient_email, subject FROM email_outbox WHERE id = $1`, outPre).Scan(&stPre, &recPre, &subjPre)
	if stPre != domain.OutboxStatusCancelled || strings.Contains(recPre+subjPre, "barrier.pre") {
		t.Fatalf("expected pre-transport row CANCELLED & scrubbed, got st=%s rec=%q subj=%q", stPre, recPre, subjPre)
	}

	// 2. Active DISPATCHING blocks AnonymizeGuest with ErrConflict while Send runs outside any DB tx,
	// persists erasure_requested_at, blocks new sends immediately, and records honest SENT outcome once unblocked.
	gInflight, outInflight := createGuestWithQueuedEmail("inflight-sent")
	dispatchDone := make(chan struct {
		sent int
		err  error
	}, 1)
	go func() {
		n, err := svc.DispatchPendingEmails(ctx)
		dispatchDone <- struct {
			sent int
			err  error
		}{n, err}
	}()

	sentMsg := <-bMailer.started
	if !strings.Contains(sentMsg.To, "barrier.inflight-sent@example.test") {
		t.Fatalf("unexpected barrier message: %+v", sentMsg)
	}
	var stMid, widMid string
	var dispatchedAtMid *time.Time
	if err := store.Pool().QueryRow(ctx, `
		SELECT status, COALESCE(dispatch_worker_id, ''), dispatched_at
		FROM email_outbox WHERE id = $1
	`, outInflight).Scan(&stMid, &widMid, &dispatchedAtMid); err != nil {
		t.Fatalf("query mid-flight outbox: %v", err)
	}
	if stMid != domain.OutboxStatusDispatching || widMid == "" || dispatchedAtMid == nil {
		t.Fatalf("expected DISPATCHING state with worker id & dispatched_at, got st=%s wid=%q dispatched=%v", stMid, widMid, dispatchedAtMid)
	}

	// Concurrent AnonymizeGuest MUST reject while DISPATCHING is active (not mark guest ANONYMIZED prematurely).
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gInflight.ID, "Mid-flight erasure request"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict while dispatch in-flight, got: %v", err)
	}
	gAfterConflict, err := store.GetGuest(ctx, nil, prop.ID, gInflight.ID)
	if err != nil {
		t.Fatalf("reload guest: %v", err)
	}
	if gAfterConflict.Status == domain.GuestStatusAnonymized || gAfterConflict.ErasureRequestedAt == nil {
		t.Fatalf("expected non-anonymized guest with erasure_requested_at set, got status=%s erasureRequestedAt=%v", gAfterConflict.Status, gAfterConflict.ErasureRequestedAt)
	}
	// New email sends must already fail once erasure_requested_at is set.
	if _, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gInflight.ID, Recipient: gInflight.Email, Subject: "Blocked", BodyText: "Blocked",
	}); err == nil {
		t.Fatalf("expected SendGuestEmail blocked while erasure_requested_at is pending")
	}

	// Unblock mailer -> succeeds cleanly -> outbox settles honestly as SENT with scrubbed PII -> AnonymizeGuest succeeds now.
	bMailer.release <- nil
	resDispatch := <-dispatchDone
	if resDispatch.err != nil || resDispatch.sent != 1 {
		t.Fatalf("expected dispatch to finish cleanly with sent=1, got sent=%d err=%v", resDispatch.sent, resDispatch.err)
	}
	var stDone, recDone, bodyDone string
	_ = store.Pool().QueryRow(ctx, `SELECT status, recipient_email, body_text FROM email_outbox WHERE id = $1`, outInflight).Scan(&stDone, &recDone, &bodyDone)
	if stDone != domain.OutboxStatusSent || strings.Contains(recDone+bodyDone, "barrier.inflight-sent") {
		t.Fatalf("expected settled SENT with scrubbed PII, got st=%s rec=%q body=%q", stDone, recDone, bodyDone)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gInflight.ID, "Finalise after send settled"); err != nil {
		t.Fatalf("finalise anonymize after settled send: %v", err)
	}

	// 3. Definite failure during concurrent erasure settles cleanly to CANCELLED with safe error category and allows final erasure.
	gFail, outFail := createGuestWithQueuedEmail("inflight-fail")
	dispatchFailDone := make(chan error, 1)
	go func() {
		_, err := svc.DispatchPendingEmails(ctx)
		dispatchFailDone <- err
	}()
	<-bMailer.started
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gFail.ID, "Definite fail barrier"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict for inflight fail case, got %v", err)
	}
	bMailer.release <- errors.New("550 5.1.1 <barrier.inflight-fail@example.test> permanent failure")
	if err := <-dispatchFailDone; err != nil {
		t.Fatalf("dispatch fail settlement error: %v", err)
	}
	var stFail, errFail, recFail string
	_ = store.Pool().QueryRow(ctx, `SELECT status, COALESCE(last_error, ''), recipient_email FROM email_outbox WHERE id = $1`, outFail).Scan(&stFail, &errFail, &recFail)
	if stFail != domain.OutboxStatusCancelled || strings.Contains(errFail+recFail, "barrier.inflight-fail") {
		t.Fatalf("expected CANCELLED + scrubbed last_error without recipient PII, got st=%s err=%q rec=%q", stFail, errFail, recFail)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gFail.ID, "Finalise after fail settled"); err != nil {
		t.Fatalf("finalise anonymize after fail: %v", err)
	}

	// 4. Stale DISPATCHING timeout transitions to DISPATCH_UNKNOWN (NOT auto-cancelled or re-sent),
	// logs DISPATCH_UNKNOWN delivery event, blocks anonymisation until reconciled via HTTP API, then settles.
	gUnknown, outUnknown := createGuestWithQueuedEmail("unknown")
	claimedUnk, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(claimedUnk) != 1 || claimedUnk[0].ID != outUnknown {
		t.Fatalf("claim unknown: %+v err=%v", claimedUnk, err)
	}
	okUnk, err := store.BeginOutboxDispatch(ctx, outUnknown, claimedUnk[0].WorkerToken, "crashed-worker-99", clk.Now().UTC())
	if err != nil || !okUnk {
		t.Fatalf("begin dispatch unknown: ok=%v err=%v", okUnk, err)
	}
	// Worker crashed after BeginOutboxDispatch; advance past 15m stale-dispatch threshold and sweep.
	clk.Advance(20 * time.Minute)
	countBeforeSweep := bMailer.Count()
	sentSweep, err := svc.DispatchPendingEmails(ctx)
	if err != nil || sentSweep != 0 || bMailer.Count() != countBeforeSweep {
		t.Fatalf("expected stale DISPATCHING to avoid resend, sentSweep=%d mailerCount=%d err=%v", sentSweep, bMailer.Count(), err)
	}
	var stUnk string
	_ = store.Pool().QueryRow(ctx, `SELECT status FROM email_outbox WHERE id = $1`, outUnknown).Scan(&stUnk)
	if stUnk != domain.OutboxStatusDispatchUnknown {
		t.Fatalf("expected stale DISPATCHING to move to DISPATCH_UNKNOWN, got %s", stUnk)
	}
	var unkEventCount int
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM communication_delivery_events WHERE email_outbox_id = $1 AND event_type = 'DISPATCH_UNKNOWN'`, outUnknown).Scan(&unkEventCount)
	if unkEventCount != 1 {
		t.Fatalf("expected 1 DISPATCH_UNKNOWN delivery event, got %d", unkEventCount)
	}
	// Anonymisation MUST still block with ErrConflict while dispatch outcome is unknown.
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gUnknown.ID, "Blocked by DISPATCH_UNKNOWN"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict on DISPATCH_UNKNOWN, got %v", err)
	}

	// Stale crashed worker token attempting late MarkOutboxEmailSentWithToken MUST be refused.
	if err := store.MarkOutboxEmailSentWithToken(ctx, outUnknown, claimedUnk[0].WorkerToken, clk.Now().UTC()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected stale worker token to be rejected with ErrConflict, got %v", err)
	}

	// Operator reconciles via HTTP API: POST /api/v1/properties/{propertyID}/outbox/{outboxID}/reconcile.
	adminClient, csrf := loginAdminClient(t, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	recBody, _ := json.Marshal(map[string]string{
		"resolution":    "CONFIRMED_FAILED",
		"reason_code":   "SMTP_PRE_DATA_RESET",
		"evidence_type": "MTA_REJECTED",
		"evidence_ref":  "mta-reset-log-8812",
		"note":          "MTA log confirmed TCP reset prior to DATA command",
	})
	recResp := authedJSON(t, adminClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/outbox/%s/reconcile", srv.URL, prop.ID, outUnknown), csrf, recBody)
	defer recResp.Body.Close()
	if recResp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 from reconcile endpoint, got %d", recResp.StatusCode)
	}
	// After reconciliation resolves DISPATCH_UNKNOWN -> FAILED (and wipes raw error once erasure_requested_at set), AnonymizeGuest completes!
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gUnknown.ID, "Finalise after operator reconciliation"); err != nil {
		t.Fatalf("anonymize after DISPATCH_UNKNOWN reconciliation: %v", err)
	}
}

func TestPhase332Hardening_QueueVsAnonymisationLockAndErrorScrubbing(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-hard-lock")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	// A1: Before-lock hook interleaving — SendGuestEmail starts BEFORE guest lock, AnonymizeGuest finishes first,
	// SendGuestEmail then acquires lock, sees ANONYMIZED, and rejects without creating outbox or communication rows.
	gRace1, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Rhiannon", LastName: "Before", Email: "race.before@example.test",
	})
	if err != nil {
		t.Fatalf("create gRace1: %v", err)
	}
	beforeReached := make(chan struct{})
	beforeRelease := make(chan struct{})
	svc.SetBeforeSendGuestLockHook(func(gid uuid.UUID) {
		if gid == gRace1.ID {
			close(beforeReached)
			<-beforeRelease
		}
	})
	sendErrCh := make(chan error, 1)
	go func() {
		_, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
			PropertyID: prop.ID, GuestID: gRace1.ID, Recipient: gRace1.Email, Subject: "Race Before", BodyText: "Never queue",
		})
		sendErrCh <- err
	}()
	<-beforeReached
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gRace1.ID, "Won race before send lock"); err != nil {
		t.Fatalf("anonymize race1: %v", err)
	}
	close(beforeRelease)
	svc.SetBeforeSendGuestLockHook(nil)
	if err := <-sendErrCh; err == nil {
		t.Fatalf("expected SendGuestEmail to fail after concurrent AnonymizeGuest won lock")
	}
	var countOutbox1, countComm1 int
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM email_outbox WHERE guest_id = $1`, gRace1.ID).Scan(&countOutbox1)
	_ = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM guest_communications WHERE guest_id = $1`, gRace1.ID).Scan(&countComm1)
	if countOutbox1 != 0 || countComm1 != 0 {
		t.Fatalf("expected 0 outbox/comm rows after rejected concurrent send, got outbox=%d comm=%d", countOutbox1, countComm1)
	}

	// A2: After-lock hook interleaving — SendGuestEmail acquires FOR UPDATE guest lock first, pauses BEFORE commit,
	// concurrent AnonymizeGuest waits for the lock, commits strictly after SendGuestEmail, and cancels/scrubs the queued row.
	gRace2, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Trystan", LastName: "After", Email: "race.after@example.test",
	})
	if err != nil {
		t.Fatalf("create gRace2: %v", err)
	}
	recMailer := &recordingMailer{}
	svc.SetEmailSender(recMailer)
	afterReached := make(chan struct{})
	afterRelease := make(chan struct{})
	svc.SetAfterSendGuestLockHook(func(gid uuid.UUID) {
		if gid == gRace2.ID {
			close(afterReached)
			<-afterRelease
		}
	})
	sendDone2 := make(chan error, 1)
	go func() {
		_, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
			PropertyID: prop.ID, GuestID: gRace2.ID, Recipient: gRace2.Email, Subject: "Race After", BodyText: "Should be cancelled cleanly",
		})
		sendDone2 <- err
	}()
	<-afterReached
	anonDone2 := make(chan error, 1)
	go func() {
		_, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gRace2.ID, "Wait on guest lock then scrub")
		anonDone2 <- err
	}()
	// Observe PostgreSQL lock contention deterministically until AnonymizeGuest is blocked
	// on the guest cluster lock held by SendGuestEmail (zero fixed-sleep timing assumptions).
	lockWaitCtx, lockWaitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer lockWaitCancel()
	lockObserved := false
	for !lockObserved {
		var blocked bool
		_ = store.Pool().QueryRow(lockWaitCtx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity a
				WHERE a.datname = current_database()
				  AND a.state = 'active'
				  AND a.wait_event_type = 'Lock'
				  AND cardinality(pg_blocking_pids(a.pid)) > 0
				  AND EXISTS (
				      SELECT 1
				      FROM pg_locks l
				      WHERE l.pid = a.pid
				        AND l.relation = 'public.guests'::regclass
				  )
			)
		`).Scan(&blocked)
		if blocked {
			lockObserved = true
			break
		}
		if lockWaitCtx.Err() != nil {
			t.Fatalf("timed out waiting for PG guest row lock contention before releasing hook")
		}
	}
	close(afterRelease)
	svc.SetAfterSendGuestLockHook(nil)
	if err := <-sendDone2; err != nil {
		t.Fatalf("expected SendGuestEmail holding lock first to commit cleanly: %v", err)
	}
	if err := <-anonDone2; err != nil {
		t.Fatalf("expected AnonymizeGuest serialized behind lock to scrub queued email cleanly: %v", err)
	}
	sent2, err := svc.DispatchPendingEmails(ctx)
	if err != nil || sent2 != 0 || recMailer.Count() != 0 {
		t.Fatalf("expected 0 emails ever dispatched after serialized anonymisation, sent=%d mailerCount=%d err=%v", sent2, recMailer.Count(), err)
	}
	var stAfter2, recAfter2 string
	_ = store.Pool().QueryRow(ctx, `SELECT status, recipient_email FROM email_outbox WHERE guest_id = $1`, gRace2.ID).Scan(&stAfter2, &recAfter2)
	if stAfter2 != domain.OutboxStatusCancelled || strings.Contains(recAfter2, "race.after") {
		t.Fatalf("expected CANCELLED & scrubbed outbox after serialized anonymize, got st=%s rec=%q", stAfter2, recAfter2)
	}

	// B: All sibling CRM mutations rejected after anonymisation + due automation job cancelled/scrubbed on sweep.
	gSib, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Nia", LastName: "Sibling", Email: "sibling.erased@example.test",
	})
	if err != nil {
		t.Fatalf("create gSib: %v", err)
	}
	// Create a template + pre-existing SCHEDULED automation job for gSib before anonymisation.
	tmpl, err := svc.CreateCommunicationTemplate(ctx, ownerActor, domain.CommunicationTemplate{
		PropertyID: prop.ID, Code: "pre_arrival_hard", Name: "Pre Arrival",
		Category: "PRE_ARRIVAL", Locale: "en", SubjectTemplate: "Welcome {{guest_full_name}}", BodyTextTemplate: "Hello {{guest_full_name}}", IsActive: true,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	rule, err := svc.CreateCommunicationAutomationRule(ctx, ownerActor, domain.CommunicationAutomationRule{
		PropertyID: prop.ID, Name: "Auto Job", TriggerEvent: domain.TriggerPreArrival, OffsetHours: 24,
		TemplateID: tmpl.ID, ApplicableStatuses: []string{"CONFIRMED"}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	units, _ := store.ListUnits(ctx, prop.ID, nil, true)
	resSib, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestID: gSib.ID, GuestName: "Nia Sibling", GuestEmail: gSib.Email,
		ChannelCode: "DIRECT", ArrivalDate: clk.Now().AddDate(0, 0, -10), DepartureDate: clk.Now().AddDate(0, 0, -8),
		Adults: 2, Status: domain.ResStatusCheckedIn, UnitIDs: []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create resSib: %v", err)
	}
	_, _ = store.Pool().Exec(ctx, `UPDATE reservations SET status = 'CHECKED_OUT' WHERE id = $1`, resSib.ID)
	// Send a real communication + outbox item for gSib, transition through DISPATCHING -> FAILED with raw SMTP error
	// to verify diagnostic error retention before anonymisation vs complete PII scrubbing afterwards.
	sibComm, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gSib.ID, ReservationID: &resSib.ID,
		Recipient: gSib.Email, Subject: "Fail Test", BodyText: "Body",
	})
	if err != nil || sibComm.EmailOutboxID == nil {
		t.Fatalf("send gSib email: comm=%+v err=%v", sibComm, err)
	}
	failedOutboxID := *sibComm.EmailOutboxID
	claimedSib, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(claimedSib) != 1 || claimedSib[0].ID != failedOutboxID {
		t.Fatalf("claim gSib outbox: %+v err=%v", claimedSib, err)
	}
	okSib, err := store.BeginOutboxDispatch(ctx, failedOutboxID, claimedSib[0].WorkerToken, "worker-sib", clk.Now().UTC())
	if err != nil || !okSib {
		t.Fatalf("begin dispatch gSib: ok=%v err=%v", okSib, err)
	}
	if err := store.MarkOutboxEmailPermanentFailureWithToken(ctx, failedOutboxID, claimedSib[0].WorkerToken, "550 5.1.1 <sibling.erased@example.test> permanent SMTP rejection"); err != nil {
		t.Fatalf("mark permanent failure gSib: %v", err)
	}
	jobID := uuid.New()
	_, err = store.Pool().Exec(ctx, `
		INSERT INTO communication_jobs (id, property_id, rule_id, reservation_id, guest_id, trigger_event, scheduled_for_utc, stay_fingerprint, status)
		VALUES ($1, $2, $3, $4, $5, 'PRE_ARRIVAL', $6, 'fp-sib-1', 'SCHEDULED')
	`, jobID, prop.ID, rule.ID, resSib.ID, gSib.ID, clk.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatalf("insert scheduled job: %v", err)
	}

	// Non-anonymized guest retains raw diagnostic last_error for troubleshooting.
	var activeLastErr string
	_ = store.Pool().QueryRow(ctx, `SELECT last_error FROM email_outbox WHERE id = $1`, failedOutboxID).Scan(&activeLastErr)
	if !strings.Contains(activeLastErr, "550 5.1.1") {
		t.Fatalf("expected active failed email to preserve diagnostic error, got %q", activeLastErr)
	}

	// Anonymize gSib -> scrubs last_error + cancels pending job, and webhook bounce later ALSO scrubs detail/last_error.
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gSib.ID, "Sibling erasure"); err != nil {
		t.Fatalf("anonymize gSib: %v", err)
	}
	var anonLastErr string
	_ = store.Pool().QueryRow(ctx, `SELECT COALESCE(last_error, '') FROM email_outbox WHERE id = $1`, failedOutboxID).Scan(&anonLastErr)
	if strings.Contains(anonLastErr, "sibling.erased") || anonLastErr != "[redacted on anonymisation]" {
		t.Fatalf("expected scrubbed last_error='[redacted on anonymisation]' after anonymisation, got %q", anonLastErr)
	}

	// Simulate delayed bounce webhook containing victim's raw email in detail/payload arriving POST-erasure.
	webhookPayload, _ := json.Marshal(service.EmailWebhookEvent{
		EmailOutboxID: &failedOutboxID,
		Provider:      "smtp-relay",
		EventType:     "BOUNCED",
		Detail:        "Remote MTA rejected <sibling.erased@example.test> unknown user",
		OccurredAt:    clk.Now().UTC(),
	})
	if err := svc.ProcessEmailDeliveryWebhook(ctx, webhookPayload, ""); err != nil {
		t.Fatalf("process delayed bounce webhook: %v", err)
	}
	var postWebhookLastErr, postWebhookEvDetail string
	_ = store.Pool().QueryRow(ctx, `SELECT COALESCE(last_error, '') FROM email_outbox WHERE id = $1`, failedOutboxID).Scan(&postWebhookLastErr)
	_ = store.Pool().QueryRow(ctx, `SELECT COALESCE(detail, '') FROM communication_delivery_events WHERE email_outbox_id = $1 AND event_type = 'BOUNCED'`, failedOutboxID).Scan(&postWebhookEvDetail)
	if strings.Contains(postWebhookLastErr+postWebhookEvDetail, "sibling.erased") {
		t.Fatalf("delayed bounce leaked PII into last_error=%q detail=%q", postWebhookLastErr, postWebhookEvDetail)
	}

	// Sibling CRM mutations on anonymised guest must ALL fail cleanly.
	if _, err := svc.AddGuestPreference(ctx, ownerActor, domain.GuestPreference{PropertyID: prop.ID, GuestID: gSib.ID, Category: domain.PrefCatAccommodation, PreferenceKey: "view", PreferenceValue: "sea"}); err == nil {
		t.Fatalf("expected AddGuestPreference blocked after anonymisation")
	}
	if _, err := svc.AddGuestNote(ctx, ownerActor, domain.GuestNote{PropertyID: prop.ID, GuestID: gSib.ID, Category: domain.NoteCatGeneral, Content: "note"}); err == nil {
		t.Fatalf("expected AddGuestNote blocked after anonymisation")
	}
	if _, err := svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{PropertyID: prop.ID, GuestID: &gSib.ID, Category: domain.EnqCatGeneral, Subject: "sub", Description: "desc"}); err == nil {
		t.Fatalf("expected CreateGuestEnquiry blocked after anonymisation")
	}
	if _, err := svc.LogGuestInteraction(ctx, ownerActor, domain.GuestCommunication{PropertyID: prop.ID, GuestID: &gSib.ID, InteractionType: domain.InteractionPhoneCall, Direction: domain.CommDirInbound, Subject: "call", BodyText: "spoke"}); err == nil {
		t.Fatalf("expected LogGuestInteraction blocked after anonymisation")
	}
	if _, err := svc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{PropertyID: prop.ID, GuestID: gSib.ID, ConsentType: "MARKETING_EMAIL", Status: domain.ConsentGranted, Method: "STAFF_ENTRY", Source: "test"}); err == nil {
		t.Fatalf("expected RecordGuestConsent blocked after anonymisation")
	}
	if err := svc.RetryFailedOutboxEmail(ctx, ownerActor, prop.ID, failedOutboxID); err == nil {
		t.Fatalf("expected RetryFailedOutboxEmail blocked after anonymisation")
	}

	// Even if a pre-existing communication_jobs row were still SCHEDULED (e.g. direct DB edge), SweepDueCommunicationJobs must skip/cancel without sending.
	gJobAnon, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Auto", LastName: "Cancelled", Email: "auto.cancelled@example.test"})
	if err != nil {
		t.Fatalf("create gJobAnon: %v", err)
	}
	resJob, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestID: gJobAnon.ID, GuestName: "Auto Cancelled", GuestEmail: gJobAnon.Email,
		ChannelCode: "DIRECT", ArrivalDate: clk.Now().AddDate(0, 0, 5), DepartureDate: clk.Now().AddDate(0, 0, 7),
		Adults: 2, Status: domain.ResStatusConfirmed, UnitIDs: []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create resJob: %v", err)
	}
	// Trigger erasure_requested_at BEFORE marking full anonymisation to verify job sweeper also refuses erasure_requested_at!
	_, _ = store.Pool().Exec(ctx, `UPDATE guests SET erasure_requested_at = NOW() WHERE id = $1`, gJobAnon.ID)
	var job2ID uuid.UUID
	if err := store.Pool().QueryRow(ctx, `
		UPDATE communication_jobs
		SET scheduled_for_utc = $2
		WHERE reservation_id = $1 AND rule_id = $3 AND status = 'SCHEDULED'
		RETURNING id
	`, resJob.ID, clk.Now().UTC().Add(-time.Hour), rule.ID).Scan(&job2ID); err != nil {
		t.Fatalf("update auto-scheduled job2 due time: %v", err)
	}
	queuedAuto, err := svc.SweepDueCommunicationJobs(ctx)
	if err != nil || queuedAuto != 0 {
		t.Fatalf("expected 0 automated sends while erasure pending, queued=%d err=%v", queuedAuto, err)
	}
	var job2St string
	_ = store.Pool().QueryRow(ctx, `SELECT status FROM communication_jobs WHERE id = $1`, job2ID).Scan(&job2St)
	if job2St != "CANCELLED" {
		t.Fatalf("expected pending job on erasure-pending guest cancelled safely, got %s", job2St)
	}
}

func TestPhase332Hardening_ExportCompletenessAndConsentRedaction(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-hard-export")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	viewer, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "viewer-hard@tirbach.wales", FullName: "Viewer", Role: domain.RoleViewer, Password: "viewer-hard-password-2026",
	})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	disabledOwner, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "disabled-owner@tirbach.wales", FullName: "Disabled Owner", Role: domain.RoleOwnerAdmin, Password: "disabled-owner-pass-2026",
	})
	if err != nil {
		t.Fatalf("create disabledOwner: %v", err)
	}
	_, _ = store.Pool().Exec(ctx, `UPDATE admin_users SET is_active = FALSE WHERE id = $1`, disabledOwner.ID)
	propOther, err := store.CreateProperty(ctx, nil, domain.Property{
		Slug: "other-hard-export", Name: "Other Property", CountryCode: "GB", Timezone: "Europe/London", Currency: "GBP",
		ContactEmail: "other-hard@example.test", CheckInTime: "15:00", CheckOutTime: "10:00",
	})
	if err != nil {
		t.Fatalf("create propOther: %v", err)
	}
	otherOwner, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "other-owner@example.test", FullName: "Other Owner", Role: domain.RoleOwnerAdmin, PropertyID: &propOther.ID, Password: "other-owner-pass-2026",
	})
	if err != nil {
		t.Fatalf("create otherOwner: %v", err)
	}

	// Build canonical guest + alias + unrelated noise guest with similar email prefix.
	gAlias, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Megan", LastName: "ExportAlias", Email: "megan.export@example.test", Phone: "07700900444",
	})
	if err != nil {
		t.Fatalf("create gAlias: %v", err)
	}
	gCanon, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Megan", LastName: "ExportCanon", Email: "megan.canon@example.test", Phone: "07700900445",
	})
	if err != nil {
		t.Fatalf("create gCanon: %v", err)
	}
	gNoise, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Megan", LastName: "Unrelated", Email: "megan.noise@example.test",
	})
	if err != nil {
		t.Fatalf("create gNoise: %v", err)
	}

	units, _ := store.ListUnits(ctx, prop.ID, nil, true)
	resAlias, err := svc.CreateReservation(ctx, ownerActor, domain.Reservation{
		PropertyID: prop.ID, GuestID: gAlias.ID, GuestName: "Megan Alias", GuestEmail: "megan.export@example.test",
		ChannelCode: "DIRECT", ArrivalDate: clk.Now().AddDate(0, 0, -10), DepartureDate: clk.Now().AddDate(0, 0, -8),
		Adults: 2, Status: domain.ResStatusCheckedIn, UnitIDs: []uuid.UUID{units[0].ID},
	})
	if err != nil {
		t.Fatalf("create resAlias: %v", err)
	}
	_, _ = store.Pool().Exec(ctx, `UPDATE reservations SET status = 'CHECKED_OUT' WHERE id = $1`, resAlias.ID)

	// Consent record containing raw caller PII in notes on gAlias before merge.
	piiConsentNote := "Opt-in confirmed via phone 07700900444 for megan.export@example.test"
	consentAlias, err := svc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{
		PropertyID: prop.ID, GuestID: gAlias.ID, ConsentType: "MARKETING_EMAIL",
		Status: domain.ConsentGranted, Method: "STAFF_ENTRY", Source: "phone_intake", Notes: piiConsentNote,
	})
	if err != nil {
		t.Fatalf("record alias consent: %v", err)
	}

	// Merge gAlias -> gCanon (verifying consent reassignment stays valid).
	if _, _, err := svc.MergeGuests(ctx, ownerActor, prop.ID, gAlias.ID, gCanon.ID, "Merged prior export"); err != nil {
		t.Fatalf("merge gAlias -> gCanon: %v", err)
	}

	// Reservation-only enquiry (guest_id IS NULL, linked solely via reservation_id = resAlias.ID) + message + communication + delivery event.
	enqResOnlyID := uuid.New()
	_, err = store.Pool().Exec(ctx, `
		INSERT INTO guest_enquiries (id, property_id, reservation_id, reference, category, status, priority, source, subject, description, assigned_user_id, created_by)
		VALUES ($1, $2, $3, 'ENQ-2026-9001', 'SPECIAL_REQUEST', 'NEW', 'NORMAL', 'ADMIN', 'Reservation-linked enquiry', 'Needs extra quiet pitch', $4, $4)
	`, enqResOnlyID, prop.ID, resAlias.ID, owner.ID)
	if err != nil {
		t.Fatalf("insert reservation-only enquiry: %v", err)
	}
	_, err = store.Pool().Exec(ctx, `
		INSERT INTO guest_enquiry_messages (id, property_id, enquiry_id, direction, sender_name, sender_email, body, is_internal, staff_user_id)
		VALUES ($1, $2, $3, 'INTERNAL', 'Sian Evans', 'owner@tirbach.wales', 'Secret staff reservation-only enquiry msg', TRUE, $4)
	`, uuid.New(), prop.ID, enqResOnlyID, owner.ID)
	if err != nil {
		t.Fatalf("insert enquiry message: %v", err)
	}
	resOnlyCommID := uuid.New()
	_, err = store.Pool().Exec(ctx, `
		INSERT INTO guest_communications (id, property_id, guest_id, reservation_id, enquiry_id, interaction_type, direction, recipient_email, subject, body_text, delivery_status, staff_user_id)
		VALUES ($1, $2, NULL, $3, $4, 'EMAIL_OUTBOUND', 'OUTBOUND', 'megan.export@example.test', 'Reservation-only comm', 'Follow-up without guest_id', 'DELIVERED', $5)
	`, resOnlyCommID, prop.ID, resAlias.ID, enqResOnlyID, owner.ID)
	if err != nil {
		t.Fatalf("insert res-only communication: %v", err)
	}
	_, err = store.Pool().Exec(ctx, `
		INSERT INTO communication_delivery_events (id, property_id, communication_id, provider, event_type, detail, occurred_at)
		VALUES ($1, $2, $3, 'smtp', 'DELIVERED', '250 OK accepted', NOW())
	`, uuid.New(), prop.ID, resOnlyCommID)
	if err != nil {
		t.Fatalf("insert delivery event: %v", err)
	}

	// Unrelated noise guest enquiry MUST NOT leak into gCanon export.
	_, err = svc.CreateGuestEnquiry(ctx, ownerActor, domain.GuestEnquiry{
		PropertyID: prop.ID, GuestID: &gNoise.ID, Category: domain.EnqCatGeneral,
		Subject: "Noise Guest Subject", Description: "Should never export for Megan Canon",
	})
	if err != nil {
		t.Fatalf("create noise enquiry: %v", err)
	}

	exp, err := svc.ExportGuestData(ctx, ownerActor, prop.ID, gCanon.ID)
	if err != nil {
		t.Fatalf("export guest data: %v", err)
	}
	foundResOnlyEnq := false
	for _, enq := range exp.Enquiries {
		if enq.ID == enqResOnlyID {
			foundResOnlyEnq = true
		}
		if strings.Contains(enq.Subject, "Noise Guest Subject") {
			t.Fatalf("cross-guest enquiry leaked into export: %+v", enq)
		}
	}
	if !foundResOnlyEnq {
		t.Fatalf("expected reservation-only enquiry %s (guest_id IS NULL) in ExportGuestData", enqResOnlyID)
	}
	foundResOnlyMsg := false
	for _, m := range exp.EnquiryMessages {
		if m.EnquiryID == enqResOnlyID {
			foundResOnlyMsg = true
			if m.StaffUserID != nil {
				t.Fatalf("expected StaffUserID minimized to nil in ExportGuestData, got %+v", m.StaffUserID)
			}
		}
	}
	if !foundResOnlyMsg {
		t.Fatalf("expected reservation-only enquiry message in ExportGuestData.EnquiryMessages, got %+v", exp.EnquiryMessages)
	}
	foundResOnlyComm := false
	for _, c := range exp.Communications {
		if c.ID == resOnlyCommID {
			foundResOnlyComm = true
			if c.StaffUserID != nil || c.StaffUserName != "" {
				t.Fatalf("expected StaffUserID/StaffUserName minimized in ExportGuestData, got %+v", c)
			}
		}
	}
	if !foundResOnlyComm {
		t.Fatalf("expected reservation-only communication %s in ExportGuestData", resOnlyCommID)
	}
	if len(exp.DeliveryEvents) != 1 || exp.DeliveryEvents[0].CommunicationID != resOnlyCommID {
		t.Fatalf("expected DeliveryEvents in ExportGuestData for comm %s, got %+v", resOnlyCommID, exp.DeliveryEvents)
	}

	// Verify PostgreSQL append-only protection on guest_consent_records and DB checks inside redact_guest_consent_note.
	if _, err := store.Pool().Exec(ctx, `UPDATE guest_consent_records SET notes = 'tamper' WHERE id = $1`, consentAlias.ID); err == nil {
		t.Fatalf("expected direct UPDATE notes on guest_consent_records to fail")
	}
	if _, err := store.Pool().Exec(ctx, `DELETE FROM guest_consent_records WHERE id = $1`, consentAlias.ID); err == nil {
		t.Fatalf("expected direct DELETE on guest_consent_records to fail")
	}
	for _, bad := range []struct {
		name  string
		actor *uuid.UUID
		why   string
	}{
		{"viewer role", &viewer.ID, "redaction test"},
		{"inactive owner", &disabledOwner.ID, "redaction test"},
		{"cross-property owner", &otherOwner.ID, "redaction test"},
		{"null actor", nil, "redaction test"},
		{"blank reason", &owner.ID, "   "},
	} {
		var outID uuid.UUID
		if err := store.Pool().QueryRow(ctx, `SELECT public.redact_guest_consent_note($1, $2, '[redacted]', $3, $4)`,
			uuid.New(), consentAlias.ID, bad.why, bad.actor).Scan(&outID); err == nil {
			t.Fatalf("expected redact_guest_consent_note to reject %s, succeeded with %s", bad.name, outID)
		}
	}

	// Anonymizing canonical guest creates overlay redaction + clean withdrawal row while preserving immutable original row!
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gCanon.ID, "Consent overlay verification"); err != nil {
		t.Fatalf("anonymize gCanon: %v", err)
	}
	expAfter, err := svc.ExportGuestData(ctx, ownerActor, prop.ID, gCanon.ID)
	if err != nil {
		t.Fatalf("export after anonymize: %v", err)
	}
	foundRedactedOverlay := false
	foundWithdrawal := false
	for _, cr := range expAfter.ConsentHistory {
		if strings.Contains(cr.Notes, "07700900444") || strings.Contains(cr.Notes, "megan.export") {
			t.Fatalf("consent export leaked unredacted note PII: %+v", cr)
		}
		if cr.ID == consentAlias.ID && cr.IsNoteRedacted && cr.NoteRedactedAt != nil {
			foundRedactedOverlay = true
		}
		if cr.Method == "ANONYMISATION" && cr.Notes == "Consent withdrawn on erasure" {
			foundWithdrawal = true
		}
	}
	if !foundRedactedOverlay || !foundWithdrawal {
		t.Fatalf("expected redacted original consent overlay + clean withdrawal row, foundRedacted=%v foundWithdrawal=%v", foundRedactedOverlay, foundWithdrawal)
	}
}

func TestPhase332Hardening_AuditFunctionSecurityRoleAndLongLineage(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-hard-role")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	staffUser, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "staff-hard-audit@tirbach.wales", FullName: "Staff", Role: domain.RoleStaff, Password: "staff-hard-audit-2026",
	})
	rotateOwner, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "rotate-owner@tirbach.wales", FullName: "Rotate", Role: domain.RoleOwnerAdmin, Password: "rotate-owner-pass-2026",
	})
	_, _ = store.Pool().Exec(ctx, `UPDATE admin_users SET must_rotate_password = TRUE WHERE id = $1`, rotateOwner.ID)
	inactiveOwner, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "inactive-owner@tirbach.wales", FullName: "Inactive", Role: domain.RoleOwnerAdmin, Password: "inactive-owner-pass-2026",
	})
	_, _ = store.Pool().Exec(ctx, `UPDATE admin_users SET is_active = FALSE WHERE id = $1`, inactiveOwner.ID)
	prop2, _ := store.CreateProperty(ctx, nil, domain.Property{
		Slug: "hard-audit-p2", Name: "Other Audit Site", CountryCode: "GB", Timezone: "Europe/London", Currency: "GBP",
		ContactEmail: "otheraudit@example.test", CheckInTime: "15:00", CheckOutTime: "10:00",
	})
	otherOwner, _ := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "other-audit-owner@example.test", FullName: "OtherAudit", Role: domain.RoleOwnerAdmin, PropertyID: &prop2.ID, Password: "other-audit-pass-2026",
	})

	// Seed a legacy audit entry on prop for redact_audit_log_entry hardening checks.
	var legacyAuditID int64
	err := store.Pool().QueryRow(ctx, `
		INSERT INTO audit_logs (property_id, actor_user_id, actor_email, actor_role, action, resource_type, resource_id, metadata)
		VALUES ($1, $2, 'legacy@example.test', 'owner_admin', 'guest.updated', 'guest', $3, '{"email":"legacy@example.test"}'::jsonb)
		RETURNING id
	`, prop.ID, owner.ID, uuid.NewString()).Scan(&legacyAuditID)
	if err != nil {
		t.Fatalf("insert legacy audit entry: %v", err)
	}

	// Verify pinned search_path on security-definer functions.
	for _, fn := range []string{"redact_audit_log_entry", "redact_guest_consent_note"} {
		var proconfig []string
		if err := store.Pool().QueryRow(ctx, `
			SELECT COALESCE(proconfig, ARRAY[]::text[]) FROM pg_proc
			WHERE pronamespace = 'public'::regnamespace AND proname = $1
		`, fn).Scan(&proconfig); err != nil {
			t.Fatalf("inspect proconfig for %s: %v", fn, err)
		}
		if !strings.Contains(strings.Join(proconfig, ","), "search_path=pg_catalog, public") {
			t.Fatalf("expected pinned search_path on %s, got %v", fn, proconfig)
		}
	}

	mustPGCode := func(err error, wantCode, label string) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected PG SQLSTATE %s, got nil", label, wantCode)
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != wantCode {
			t.Fatalf("%s: expected PG SQLSTATE %s, got %v (code=%v)", label, wantCode, err, func() string {
				if pgErr != nil {
					return pgErr.Code
				}
				return "non-pg"
			}())
		}
	}

	// Create a real consent record up front so privilege denial tests reference real valid FKs and column names.
	seedConsentGuest, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "PermCheck", LastName: "Guest", Email: "perm.check@example.test",
	})
	if err != nil {
		t.Fatalf("create seedConsentGuest: %v", err)
	}
	seedConsent, err := svc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{
		PropertyID: prop.ID, GuestID: seedConsentGuest.ID, ConsentType: "MARKETING_EMAIL",
		Status: domain.ConsentGranted, Method: "STAFF_ENTRY", Source: "desk", Notes: "Original consent note",
	})
	if err != nil {
		t.Fatalf("create seedConsent: %v", err)
	}

	// Direct PostgreSQL checks inside redact_audit_log_entry must block unauthorized/disabled/rotating/foreign actors
	// with SQLSTATE 42501, and reject invalid/PII-smuggling reasons with SQLSTATE 22023.
	for _, bad := range []struct {
		name     string
		actor    *uuid.UUID
		why      string
		wantCode string
	}{
		{"staff role", &staffUser.ID, "Valid redaction reason", "42501"},
		{"must_rotate_password=true", &rotateOwner.ID, "Valid redaction reason", "42501"},
		{"inactive owner", &inactiveOwner.ID, "Valid redaction reason", "42501"},
		{"cross-property owner", &otherOwner.ID, "Valid redaction reason", "42501"},
		{"null actor", nil, "Valid redaction reason", "42501"},
		{"unknown actor", func() *uuid.UUID { u := uuid.New(); return &u }(), "Valid redaction reason", "42501"},
		{"blank reason", &owner.ID, "   ", "22023"},
		{"email PII in reason", &owner.ID, "Remove leak@example.com from audit", "22023"},
	} {
		var rid uuid.UUID
		err := store.Pool().QueryRow(ctx, `
			SELECT public.redact_audit_log_entry($1, $2, NULL, NULL, '{"redacted":true}'::jsonb, $3, $4)
		`, uuid.New(), legacyAuditID, bad.why, bad.actor).Scan(&rid)
		mustPGCode(err, bad.wantCode, "redact_audit_log_entry "+bad.name)
	}

	// Direct PostgreSQL checks inside redact_guest_consent_note must enforce same actor/scope rules plus
	// canonical replacement placeholder allowlist and anti-PII-smuggling reason checks.
	for _, bad := range []struct {
		name     string
		notes    string
		why      string
		actor    *uuid.UUID
		wantCode string
	}{
		{"staff role", "[Redacted on guest anonymisation]", "Valid reason", &staffUser.ID, "42501"},
		{"cross-property owner", "[Redacted on guest anonymisation]", "Valid reason", &otherOwner.ID, "42501"},
		{"arbitrary replacement note smuggling", "call +44 7700 900999", "Valid reason", &owner.ID, "22023"},
		{"PII email in reason", "[Redacted on guest anonymisation]", "Guest guest@example.test erased", &owner.ID, "22023"},
	} {
		var rid uuid.UUID
		err := store.Pool().QueryRow(ctx, `
			SELECT public.redact_guest_consent_note($1, $2, $3, $4, $5)
		`, uuid.New(), seedConsent.ID, bad.notes, bad.why, bad.actor).Scan(&rid)
		mustPGCode(err, bad.wantCode, "redact_guest_consent_note "+bad.name)
	}

	// Ensure lovelace_app exists and receives the exact runtime DML + lockdown grants on this test DB,
	// then connect a real pgxpool as lovelace_app and prove direct table bypasses fail with 42501 while runtime succeeds!
	var hasAppRole bool
	if err := store.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lovelace_app')`).Scan(&hasAppRole); err != nil {
		t.Fatalf("check lovelace_app role: %v", err)
	}
	if !hasAppRole {
		t.Fatalf("lovelace_app role not found on PG17 test cluster")
	}
	baseURL := os.Getenv("TEST_DATABASE_URL")
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	dbName := strings.TrimPrefix(parsed.Path, "/")
	grantSQL := fmt.Sprintf(`
		GRANT CONNECT ON DATABASE %q TO lovelace_app;
		GRANT USAGE ON SCHEMA public TO lovelace_app;
		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO lovelace_app;
		GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO lovelace_app;
		GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO lovelace_app;
		REVOKE ALL ON TABLE public.audit_logs FROM PUBLIC, lovelace_app;
		GRANT INSERT ON TABLE public.audit_logs TO lovelace_app;
		GRANT USAGE, SELECT ON SEQUENCE public.audit_logs_id_seq TO lovelace_app;
		GRANT SELECT ON TABLE public.audit_logs_effective TO lovelace_app;
		REVOKE ALL ON TABLE public.audit_log_redactions FROM PUBLIC, lovelace_app;
		REVOKE ALL ON TABLE public.guest_consent_redactions FROM PUBLIC, lovelace_app;
		REVOKE ALL ON TABLE public.guest_consent_records FROM PUBLIC, lovelace_app;
		GRANT SELECT, INSERT ON TABLE public.guest_consent_records TO lovelace_app;
		GRANT UPDATE (guest_id) ON TABLE public.guest_consent_records TO lovelace_app;
		GRANT SELECT ON TABLE public.guest_consent_records_effective TO lovelace_app;
		REVOKE ALL ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) FROM PUBLIC;
		GRANT EXECUTE ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) TO lovelace_app;
		REVOKE ALL ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) FROM PUBLIC;
		GRANT EXECUTE ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) TO lovelace_app;
	`, dbName)
	if _, err := store.Pool().Exec(ctx, grantSQL); err != nil {
		t.Fatalf("apply lovelace_app grants on %s: %v", dbName, err)
	}

	appURL := os.Getenv("TEST_APP_DATABASE_URL")
	if appURL == "" {
		appParsed := *parsed
		appParsed.User = url.UserPassword("lovelace_app", "lovelace_app_dev_pw")
		appURL = appParsed.String()
	}
	appPool, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("connect as lovelace_app: %v", err)
	}
	defer appPool.Close()

	var currentUser string
	var super, createDB, createRole bool
	if err := appPool.QueryRow(ctx, `SELECT current_user, r.rolsuper, r.rolcreatedb, r.rolcreaterole FROM pg_roles r WHERE r.rolname = current_user`).Scan(&currentUser, &super, &createDB, &createRole); err != nil {
		t.Fatalf("verify lovelace_app role identity: %v", err)
	}
	if currentUser != "lovelace_app" || super || createDB || createRole {
		t.Fatalf("expected non-privileged lovelace_app session, got user=%s super=%v createDB=%v createRole=%v", currentUser, super, createDB, createRole)
	}

	// Prove direct raw-table privilege boundaries fail under lovelace_app with exact SQLSTATE 42501
	// using schema-valid columns and valid FK IDs (never passing on 42703/23503/42601 errors).
	deniedQueries := []struct {
		name string
		sql  string
	}{
		{"SELECT audit_logs", `SELECT COUNT(*) FROM public.audit_logs`},
		{"UPDATE audit_logs", `UPDATE public.audit_logs SET action = 'tamper'`},
		{"DELETE audit_logs", `DELETE FROM public.audit_logs`},
		{"SELECT audit_log_redactions", `SELECT COUNT(*) FROM public.audit_log_redactions`},
		{"INSERT audit_log_redactions", fmt.Sprintf(
			`INSERT INTO public.audit_log_redactions (id, audit_log_id, property_id, redacted_metadata, reason, reason_category, redacted_by) VALUES ('%s', %d, '%s', '{"redacted":true}'::jsonb, 'Direct bypass attempt', 'HISTORICAL_PII_MASK', '%s')`,
			uuid.NewString(), legacyAuditID, prop.ID.String(), owner.ID.String(),
		)},
		{"UPDATE audit_log_redactions", `UPDATE public.audit_log_redactions SET reason = 'tamper'`},
		{"DELETE audit_log_redactions", `DELETE FROM public.audit_log_redactions`},
		{"SELECT guest_consent_redactions", `SELECT COUNT(*) FROM public.guest_consent_redactions`},
		{"INSERT guest_consent_redactions", fmt.Sprintf(
			`INSERT INTO public.guest_consent_redactions (id, consent_record_id, property_id, redacted_notes, reason, reason_category, redacted_by) VALUES ('%s', '%s', '%s', '[redacted]', 'Direct bypass attempt', 'GUEST_ERASURE', '%s')`,
			uuid.NewString(), seedConsent.ID.String(), prop.ID.String(), owner.ID.String(),
		)},
		{"UPDATE guest_consent_redactions", `UPDATE public.guest_consent_redactions SET reason = 'tamper'`},
		{"DELETE guest_consent_redactions", `DELETE FROM public.guest_consent_redactions`},
		{"UPDATE guest_consent_records.notes", `UPDATE public.guest_consent_records SET notes = 'tamper'`},
		{"DELETE guest_consent_records", `DELETE FROM public.guest_consent_records`},
	}
	for _, dq := range deniedQueries {
		_, err := appPool.Exec(ctx, dq.sql)
		mustPGCode(err, "42501", "lovelace_app "+dq.name)
	}

	// Prove Security Definer execution via lovelace_app still enforces SQLSTATE 42501 for unauthorized
	// or cross-property callers, never relying on runtime role alone.
	var deniedFnID uuid.UUID
	fnDenyErr := appPool.QueryRow(ctx, `
		SELECT public.redact_audit_log_entry($1, $2, NULL, NULL, '{"redacted":true}'::jsonb, 'Valid cross prop test', $3)
	`, uuid.New(), legacyAuditID, otherOwner.ID).Scan(&deniedFnID)
	mustPGCode(fnDenyErr, "42501", "lovelace_app cross-property redact_audit_log_entry")

	// Construct real app Store + Service backed by lovelace_app pool and prove normal runtime workflows work!
	appStore, err := postgres.New(ctx, appURL)
	if err != nil {
		t.Fatalf("postgres.New as lovelace_app: %v", err)
	}
	defer appStore.Close()
	appCfg := config.Config{
		AppEnv: "test", DatabaseURL: appURL, EncryptionKey: testEncryptionKey,
		SessionTTL: time.Hour, SessionIdleTTL: 30 * time.Minute, EmailFrom: "bookings@tirbach.wales",
	}
	appSvc := service.New(appStore, appCfg)
	appSvc.SetClock(clk.Now)

	if _, err := appSvc.RedactHistoricalAuditLog(ctx, ownerActor, prop.ID, legacyAuditID, "Legacy raw email removal"); err != nil {
		t.Fatalf("RedactHistoricalAuditLog via lovelace_app failed: %v", err)
	}
	logs, err := appSvc.ListAuditLogs(ctx, ownerActor, &prop.ID, "", 50)
	if err != nil || len(logs) == 0 {
		t.Fatalf("ListAuditLogs via lovelace_app failed: len=%d err=%v", len(logs), err)
	}

	// >16-hop merge lineage test executed cleanly via runtime lovelace_app service!
	// Chain g[0] -> g[1] -> ... -> g[20] (20 merge hops!), plus side branch gSide -> g[5].
	var chain [21]domain.Guest
	for i := 0; i < 21; i++ {
		g, err := appSvc.CreateGuest(ctx, ownerActor, domain.Guest{
			PropertyID: prop.ID,
			FirstName:  fmt.Sprintf("Chain%02d", i),
			LastName:   "Lineage",
			Email:      fmt.Sprintf("chain.%02d@example.test", i),
		})
		if err != nil {
			t.Fatalf("create chain[%d]: %v", i, err)
		}
		chain[i] = g
	}
	gSide, err := appSvc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Side", LastName: "Branch", Email: "chain.side@example.test",
	})
	if err != nil {
		t.Fatalf("create gSide: %v", err)
	}
	if _, err := appSvc.AddGuestPreference(ctx, ownerActor, domain.GuestPreference{
		PropertyID: prop.ID, GuestID: chain[0].ID, Category: domain.PrefCatAccommodation, PreferenceKey: "origin", PreferenceValue: "hop-00",
	}); err != nil {
		t.Fatalf("add hop-00 preference: %v", err)
	}
	if _, err := appSvc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{
		PropertyID: prop.ID, GuestID: gSide.ID, ConsentType: "MARKETING_EMAIL",
		Status: domain.ConsentGranted, Method: "STAFF_ENTRY", Source: "side", Notes: "Side secret note",
	}); err != nil {
		t.Fatalf("add gSide consent: %v", err)
	}

	for i := 0; i < 20; i++ {
		if i == 5 {
			if _, _, err := appSvc.MergeGuests(ctx, ownerActor, prop.ID, gSide.ID, chain[5].ID, "Side branch into hop 5"); err != nil {
				t.Fatalf("merge gSide into chain[5]: %v", err)
			}
		}
		if _, _, err := appSvc.MergeGuests(ctx, ownerActor, prop.ID, chain[i].ID, chain[i+1].ID, fmt.Sprintf("Hop %d->%d", i, i+1)); err != nil {
			t.Fatalf("merge hop %d->%d: %v", i, i+1, err)
		}
	}

	// PostgreSQL resolve_canonical_guest_id AND Go ResolveCanonicalGuestID both traverse all 20 hops.
	var pgCanon uuid.UUID
	if err := appPool.QueryRow(ctx, `SELECT public.resolve_canonical_guest_id($1, $2)`, prop.ID, chain[0].ID).Scan(&pgCanon); err != nil {
		t.Fatalf("pg resolve_canonical_guest_id: %v", err)
	}
	if pgCanon != chain[20].ID {
		t.Fatalf("expected pg canonical=%s after 20 hops, got %s", chain[20].ID, pgCanon)
	}
	goCanon, err := appStore.ResolveCanonicalGuestID(ctx, nil, prop.ID, chain[0].ID)
	if err != nil || goCanon != chain[20].ID {
		t.Fatalf("expected Go canonical=%s after 20 hops, got %s err=%v", chain[20].ID, goCanon, err)
	}

	expDeep, err := appSvc.ExportGuestData(ctx, ownerActor, prop.ID, chain[20].ID)
	if err != nil {
		t.Fatalf("ExportGuestData on 20-hop canonical: %v", err)
	}
	if len(expDeep.MergeHistory) != 21 {
		t.Fatalf("expected all 21 merge events (20 linear + 1 side branch), got %d", len(expDeep.MergeHistory))
	}
	foundOriginPref := false
	for _, p := range expDeep.Preferences {
		if p.PreferenceKey == "origin" && p.PreferenceValue == "hop-00" {
			foundOriginPref = true
		}
	}
	if !foundOriginPref {
		t.Fatalf("expected hop-00 preference in deep canonical export, got %+v", expDeep.Preferences)
	}

	// Anonymize the 22-guest canonical cluster via lovelace_app!
	if _, err := appSvc.AnonymizeGuest(ctx, ownerActor, prop.ID, chain[20].ID, "Erase full 22-guest deep cluster"); err != nil {
		t.Fatalf("anonymize deep cluster via lovelace_app: %v", err)
	}
	var unanonymizedCount int
	_ = store.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM guests
		WHERE property_id = $1 AND last_name IN ('Lineage', 'Branch')
		  AND (anonymized_at IS NULL OR email LIKE '%chain.%')
	`, prop.ID).Scan(&unanonymizedCount)
	if unanonymizedCount != 0 {
		t.Fatalf("expected all 22 cluster guests scrubbed & anonymized, found %d remaining", unanonymizedCount)
	}
}

func TestPhase332Hardening_WorkerLivenessFencingAndSMTPDeadlines(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-liveness")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{
		UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role,
		IPAddress: "127.0.0.1", UserAgent: "liveness-test",
	}

	// 1. Live worker heartbeating cleanly MUST NOT be swept or released even when dispatched_at > 5 minutes ago.
	svc.SetClock(time.Now)
	if err := svc.SetDispatchConfig(service.DispatchConfig{
		WorkerID:          "live-worker",
		LeaseTTL:          time.Second,
		HeartbeatInterval: 25 * time.Millisecond,
		TransportTimeout:  900 * time.Millisecond,
	}); err != nil {
		t.Fatalf("SetDispatchConfig live-worker: %v", err)
	}
	liveMailer := newBarrierMailer()
	svc.SetEmailSender(liveMailer)

	gLive, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Live", LastName: "Heartbeater", Email: "live.hb@example.test",
	})
	if err != nil {
		t.Fatalf("create gLive: %v", err)
	}
	commLive, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gLive.ID, Recipient: gLive.Email,
		Subject: "Slow Live Transport", BodyText: "Held open while heartbeats renew",
	})
	if err != nil || commLive.EmailOutboxID == nil {
		t.Fatalf("queue gLive email: comm=%+v err=%v", commLive, err)
	}
	outLiveID := *commLive.EmailOutboxID

	liveDone := make(chan int, 1)
	go func() {
		n, _ := svc.DispatchPendingEmails(ctx)
		liveDone <- n
	}()
	select {
	case <-liveMailer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for liveMailer.started")
	}

	// Backdate dispatched_at > 10m ago while waiting for real heartbeats to advance dispatch_heartbeat_at.
	if _, err := store.Pool().Exec(ctx, `UPDATE email_outbox SET dispatched_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, outLiveID); err != nil {
		t.Fatalf("backdate dispatched_at: %v", err)
	}
	var initialHB time.Time
	_ = store.Pool().QueryRow(ctx, `SELECT dispatch_heartbeat_at FROM email_outbox WHERE id = $1`, outLiveID).Scan(&initialHB)
	waitHB, cancelHB := context.WithTimeout(ctx, 2*time.Second)
	defer cancelHB()
	for {
		var curHB time.Time
		_ = store.Pool().QueryRow(waitHB, `SELECT dispatch_heartbeat_at FROM email_outbox WHERE id = $1`, outLiveID).Scan(&curHB)
		if curHB.After(initialHB) {
			break
		}
		if waitHB.Err() != nil {
			t.Fatalf("heartbeat never renewed after initialHB=%s", initialHB)
		}
	}

	// Sweep AND a competing worker calling DispatchPendingEmails MUST NOT fence or steal the live heartbeating item!
	fencedWhileLive, err := store.SweepStaleOutboxDispatches(ctx, time.Now().UTC())
	if err != nil || len(fencedWhileLive) != 0 {
		t.Fatalf("expected live heartbeating dispatch NOT to be fenced past >5m dispatched_at, got fenced=%v err=%v", fencedWhileLive, err)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gLive.ID, "Blocked by live heartbeating dispatch"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected AnonymizeGuest blocked while live worker heartbeating, got %v", err)
	}

	// Release live send and confirm clean completion -> SENT + erasure succeeds.
	liveMailer.release <- nil
	if sent := <-liveDone; sent != 1 {
		t.Fatalf("expected live worker to complete 1 send, got %d", sent)
	}
	var stLive, outcomeLive string
	_ = store.Pool().QueryRow(ctx, `SELECT status, dispatch_outcome FROM email_outbox WHERE id = $1`, outLiveID).Scan(&stLive, &outcomeLive)
	if stLive != domain.OutboxStatusSent || outcomeLive != "PROVIDER_ACCEPTED" {
		t.Fatalf("expected SENT / PROVIDER_ACCEPTED, got status=%s outcome=%s", stLive, outcomeLive)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gLive.ID, "Finalise after live send settled"); err != nil {
		t.Fatalf("anonymize after live send settled: %v", err)
	}

	// 2.Hung/stopped worker whose lease expires IS fenced -> DISPATCH_UNKNOWN, cannot renew, cannot overwrite late.
	svc.SetClock(clk.Now)
	if err := svc.SetDispatchConfig(service.DispatchConfig{
		WorkerID:          "hung-worker",
		LeaseTTL:          60 * time.Second,
		HeartbeatInterval: 20 * time.Second,
		TransportTimeout:  30 * time.Second,
	}); err != nil {
		t.Fatalf("SetDispatchConfig hung-worker: %v", err)
	}
	hungMailer := newBarrierMailer()
	hungMailer.ignoreCtx = true
	svc.SetEmailSender(hungMailer)

	gHung, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Hung", LastName: "Worker", Email: "hung.worker@example.test",
	})
	if err != nil {
		t.Fatalf("create gHung: %v", err)
	}
	commHung, _ := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gHung.ID, Recipient: gHung.Email, Subject: "Hung Subject", BodyText: "Body",
	})
	outHungID := *commHung.EmailOutboxID

	hungDone := make(chan struct{})
	go func() {
		_, _ = svc.DispatchPendingEmails(ctx)
		close(hungDone)
	}()
	<-hungMailer.started

	var tokHung uuid.UUID
	var epochHung int64
	var widHung string
	_ = store.Pool().QueryRow(ctx, `SELECT worker_token, dispatch_fence_epoch, dispatch_worker_id FROM email_outbox WHERE id = $1`, outHungID).Scan(&tokHung, &epochHung, &widHung)

	clk.Advance(90 * time.Second)
	fencedHung, err := store.SweepStaleOutboxDispatches(ctx, clk.Now().UTC())
	if err != nil || len(fencedHung) != 1 || fencedHung[0] != outHungID {
		t.Fatalf("expected expired lease on %s to be fenced, got %v err=%v", outHungID, fencedHung, err)
	}

	// Fenced worker attempting heartbeat renewal MUST get renewed=false.
	renewed, err := store.RenewOutboxDispatchHeartbeat(ctx, outHungID, &tokHung, widHung, epochHung, clk.Now().UTC(), 60*time.Second)
	if err != nil || renewed {
		t.Fatalf("expected fenced worker heartbeat renewal=false, got renewed=%v err=%v", renewed, err)
	}

	// Late release of hungMailer MUST NOT overwrite DISPATCH_UNKNOWN or emit ACCEPTED!
	hungMailer.release <- nil
	<-hungDone
	var stHung, outcomeHung string
	var fencedAtPresent bool
	_ = store.Pool().QueryRow(ctx, `SELECT status, dispatch_outcome, dispatch_fenced_at IS NOT NULL FROM email_outbox WHERE id = $1`, outHungID).Scan(&stHung, &outcomeHung, &fencedAtPresent)
	if stHung != domain.OutboxStatusDispatchUnknown || outcomeHung != "WORKER_FENCED" || !fencedAtPresent {
		t.Fatalf("expected fenced row to remain DISPATCH_UNKNOWN / WORKER_FENCED, got status=%s outcome=%s fenced=%v", stHung, outcomeHung, fencedAtPresent)
	}

	// 3. Real SMTPSender wired in Service:
	// (a) Post-DATA stalled MTA hits TransportTimeout -> aborted immediately, lands in DISPATCH_UNKNOWN with UNCERTAIN_TIMEOUT, blocks erasure!
	lnPost, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen post-DATA: %v", err)
	}
	defer lnPost.Close()
	postDot := make(chan struct{})
	postStop := make(chan struct{})
	defer close(postStop)
	go func() {
		conn, aErr := lnPost.Accept()
		if aErr != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("220 localhost ESMTP\r\n"))
		r := bufio.NewReader(conn)
		inData := false
		for {
			line, rErr := r.ReadString('\n')
			if rErr != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					close(postDot)
					<-postStop
					return
				}
				continue
			}
			up := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(up, "EHLO") || strings.HasPrefix(up, "HELO"):
				_, _ = conn.Write([]byte("250-localhost\r\n250 OK\r\n"))
			case strings.HasPrefix(up, "MAIL FROM:") || strings.HasPrefix(up, "RCPT TO:"):
				_, _ = conn.Write([]byte("250 OK\r\n"))
			case strings.HasPrefix(up, "DATA"):
				inData = true
				_, _ = conn.Write([]byte("354 End data\r\n"))
			}
		}
	}()

	svc.SetClock(time.Now)
	if err := svc.SetDispatchConfig(service.DispatchConfig{
		WorkerID:          "smtp-worker",
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		TransportTimeout:  120 * time.Millisecond,
	}); err != nil {
		t.Fatalf("SetDispatchConfig smtp-worker: %v", err)
	}
	svc.SetEmailSender(&mailer.SMTPSender{Addr: lnPost.Addr().String(), Timeout: 120 * time.Millisecond})

	gPost, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Post", LastName: "Timeout", Email: "post.timeout@example.test",
	})
	commPost, _ := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gPost.ID, Recipient: gPost.Email, Subject: "Post-DATA Stall", BodyText: "Body",
	})
	outPostID := *commPost.EmailOutboxID

	if _, err := svc.DispatchPendingEmails(ctx); err != nil {
		t.Fatalf("DispatchPendingEmails post-DATA: %v", err)
	}
	<-postDot
	var stPost, outPost string
	_ = store.Pool().QueryRow(ctx, `SELECT status, dispatch_outcome FROM email_outbox WHERE id = $1`, outPostID).Scan(&stPost, &outPost)
	if stPost != domain.OutboxStatusDispatchUnknown || outPost != "UNCERTAIN_TIMEOUT" {
		t.Fatalf("expected post-DATA SMTP timeout to stay DISPATCH_UNKNOWN/UNCERTAIN_TIMEOUT, got status=%s outcome=%s", stPost, outPost)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gPost.ID, "Should block while uncertain"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected AnonymizeGuest blocked by DISPATCH_UNKNOWN post-DATA timeout, got %v", err)
	}

	// (b) Pre-DATA stalled MTA + concurrent guest erasure aborts cleanly before DATA -> CANCELLED immediately!
	lnPre, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen pre-DATA: %v", err)
	}
	defer lnPre.Close()
	preStop := make(chan struct{})
	defer close(preStop)
	preStalled := make(chan struct{})
	go func() {
		conn, aErr := lnPre.Accept()
		if aErr != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("220 localhost ESMTP\r\n"))
		r := bufio.NewReader(conn)
		for {
			line, rErr := r.ReadString('\n')
			if rErr != nil {
				return
			}
			up := strings.ToUpper(line)
			if strings.HasPrefix(up, "EHLO") || strings.HasPrefix(up, "HELO") {
				_, _ = conn.Write([]byte("250-localhost\r\n250 OK\r\n"))
			} else if strings.HasPrefix(up, "MAIL FROM:") {
				close(preStalled)
				<-preStop
				return
			}
		}
	}()
	svc.SetEmailSender(&mailer.SMTPSender{Addr: lnPre.Addr().String(), Timeout: 120 * time.Millisecond})
	gPre, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Pre", LastName: "Abort", Email: "pre.abort@example.test",
	})
	commPre, _ := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gPre.ID, Recipient: gPre.Email, Subject: "Pre-DATA Stall", BodyText: "Body",
	})
	outPreID := *commPre.EmailOutboxID

	preDispatchDone := make(chan struct{})
	go func() {
		_, _ = svc.DispatchPendingEmails(ctx)
		close(preDispatchDone)
	}()
	<-preStalled
	// Trigger erasure request while SMTP transport is stalled BEFORE DATA.
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gPre.ID, "Erase while pre-DATA stalled"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict setting erasure_requested_at while DISPATCHING, got %v", err)
	}
	<-preDispatchDone
	var stPre, outPre string
	_ = store.Pool().QueryRow(ctx, `SELECT status, dispatch_outcome FROM email_outbox WHERE id = $1`, outPreID).Scan(&stPre, &outPre)
	if stPre != domain.OutboxStatusCancelled || outPre != "TERMINATED_BEFORE_SEND" {
		t.Fatalf("expected pre-DATA aborted send with erasure request to become CANCELLED / TERMINATED_BEFORE_SEND, got status=%s outcome=%s", stPre, outPre)
	}
	if _, err := svc.AnonymizeGuest(ctx, ownerActor, prop.ID, gPre.ID, "Finalise cleanly after pre-DATA abort"); err != nil {
		t.Fatalf("expected anonymize to succeed once aborted pre-DATA, got %v", err)
	}
}

func TestPhase332Hardening_ReconcileRBACEvidenceUIAndValidation(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-reconcile")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{
		UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role,
		IPAddress: "127.0.0.1", UserAgent: "reconcile-test",
	}

	staffUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "rec.staff@tirbach.wales", FullName: "Rec Staff", Role: domain.RoleStaff, Password: "rec-staff-password-2026",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	mgrUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "rec.mgr@tirbach.wales", FullName: "Rec Manager", Role: domain.RoleManager, Password: "rec-mgr-password-2026",
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	otherProp, err := store.CreateProperty(ctx, nil, domain.Property{
		Slug: "rec-other-prop", Name: "Other Prop", CountryCode: "GB", Currency: "GBP", Timezone: "Europe/London",
		ContactEmail: "rec-other-prop@example.test", CheckInTime: "14:00", CheckOutTime: "10:00",
	})
	if err != nil {
		t.Fatalf("create otherProp: %v", err)
	}
	_, err = svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &otherProp.ID, Email: "rec.other@tirbach.wales", FullName: "Other Owner", Role: domain.RoleOwnerAdmin, Password: "rec-other-password-2026",
	})
	if err != nil {
		t.Fatalf("create other owner: %v", err)
	}

	// Create 3 items: one live DISPATCHING, one DISPATCH_UNKNOWN for API tests, one DISPATCH_UNKNOWN for UI manager test.
	createUnknownOutbox := func(tag string, makeUnknown bool) uuid.UUID {
		g, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
			PropertyID: prop.ID, FirstName: "Rec", LastName: tag, Email: fmt.Sprintf("rec.%s@example.test", tag),
		})
		if err != nil {
			t.Fatalf("create guest %s: %v", tag, err)
		}
		comm, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
			PropertyID: prop.ID, GuestID: g.ID, Recipient: g.Email, Subject: "Reconcile " + tag, BodyText: "Body " + tag,
		})
		if err != nil || comm.EmailOutboxID == nil {
			t.Fatalf("queue email %s: %v", tag, err)
		}
		oid := *comm.EmailOutboxID
		claimed, _ := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
		if len(claimed) == 0 {
			t.Fatalf("expected claimed row for %s", tag)
		}
		ok, err := store.BeginOutboxDispatch(ctx, oid, claimed[0].WorkerToken, "rec-worker-"+tag, clk.Now().UTC())
		if err != nil || !ok {
			t.Fatalf("begin dispatch %s: ok=%v err=%v", tag, ok, err)
		}
		if makeUnknown {
			clk.Advance(90 * time.Second)
			fenced, err := store.SweepStaleOutboxDispatches(ctx, clk.Now().UTC())
			if err != nil || len(fenced) == 0 {
				t.Fatalf("sweep unknown %s: fenced=%v err=%v", tag, fenced, err)
			}
		}
		return oid
	}

	outUnkAPI := createUnknownOutbox("api", true)
	outUnkUI := createUnknownOutbox("ui", true)
	outLive := createUnknownOutbox("live", false)

	staffClient, staffCSRF := loginAdminClient(t, srv.URL, staffUser.Email, "rec-staff-password-2026")
	mgrClient, mgrCSRF := loginAdminClient(t, srv.URL, mgrUser.Email, "rec-mgr-password-2026")
	otherClient, otherCSRF := loginAdminClient(t, srv.URL, "rec.other@tirbach.wales", "rec-other-password-2026")
	ownerClient, ownerCSRF := loginAdminClient(t, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")

	// 1. RBAC & UI gating:
	// - Staff HTML view hides reconciliation button/form (even on DISPATCH_UNKNOWN items) AND hides Retry Send on DISPATCH_UNKNOWN.
	staffSettingsResp, err := staffClient.Get(srv.URL + "/communications/settings")
	if err != nil {
		t.Fatalf("staff GET /communications/settings: %v", err)
	}
	staffHTMLBytes, _ := io.ReadAll(staffSettingsResp.Body)
	staffSettingsResp.Body.Close()
	staffHTML := string(staffHTMLBytes)
	if strings.Contains(staffHTML, "Reconcile Unknown Dispatch") || strings.Contains(staffHTML, "/communications/outbox/") {
		t.Fatalf("RoleStaff must NOT see reconcile form or action in /communications/settings HTML")
	}
	if strings.Contains(staffHTML, "/communications/failed/"+outUnkUI.String()+"/retry") {
		t.Fatalf("DISPATCH_UNKNOWN item must NOT expose ordinary Retry Send action")
	}

	// - Manager HTML view renders reconciliation form cleanly.
	mgrSettingsResp, err := mgrClient.Get(srv.URL + "/communications/settings")
	if err != nil {
		t.Fatalf("manager GET /communications/settings: %v", err)
	}
	mgrHTMLBytes, _ := io.ReadAll(mgrSettingsResp.Body)
	mgrSettingsResp.Body.Close()
	if !strings.Contains(string(mgrHTMLBytes), "/communications/outbox/"+outUnkUI.String()+"/reconcile") {
		t.Fatalf("RoleManager must see reconcile form for DISPATCH_UNKNOWN item")
	}

	// - Staff API POST & WebUI POST -> 403 Forbidden.
	validPayload, _ := json.Marshal(map[string]string{
		"resolution": "CONFIRMED_FAILED", "reason_code": "SMTP_TCP_RESET", "evidence_type": "MTA_REJECTED",
		"evidence_ref": "mta-7741", "note": "MTA reset confirmed before DATA",
	})
	staffAPIResp := authedJSON(t, staffClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/outbox/%s/reconcile", srv.URL, prop.ID, outUnkAPI), staffCSRF, validPayload)
	staffAPIResp.Body.Close()
	if staffAPIResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected RoleStaff API reconcile -> 403, got %d", staffAPIResp.StatusCode)
	}
	form := url.Values{
		"csrf_token": {staffCSRF}, "resolution": {"CONFIRMED_FAILED"}, "reason_code": {"SMTP_TCP_RESET"},
		"evidence_type": {"MTA_REJECTED"}, "evidence_ref": {"mta-7741"}, "note": {"MTA reset confirmed before DATA"},
	}
	staffUIResp, err := staffClient.PostForm(fmt.Sprintf("%s/communications/outbox/%s/reconcile", srv.URL, outUnkUI), form)
	if err != nil {
		t.Fatalf("staff UI post: %v", err)
	}
	staffUIResp.Body.Close()
	if staffUIResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected RoleStaff WebUI reconcile -> 403, got %d", staffUIResp.StatusCode)
	}

	// - Cross-property Owner -> 403 Forbidden.
	otherResp := authedJSON(t, otherClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/outbox/%s/reconcile", srv.URL, prop.ID, outUnkAPI), otherCSRF, validPayload)
	otherResp.Body.Close()
	if otherResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected cross-property owner reconcile -> 403, got %d", otherResp.StatusCode)
	}

	// 2. Live dispatch guard: reconciling an item actively in DISPATCHING -> 409 Conflict.
	liveResp := authedJSON(t, ownerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/outbox/%s/reconcile", srv.URL, prop.ID, outLive), ownerCSRF, validPayload)
	liveResp.Body.Close()
	if liveResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected live DISPATCHING reconcile -> 409 Conflict, got %d", liveResp.StatusCode)
	}

	// 3. Evidence & anti-PII-smuggling validation -> 422 Unprocessable Entity.
	badPayloads := []map[string]string{
		{"resolution": "CANCEL", "reason_code": "FORCE_CANCEL", "evidence_type": "CONSERVATIVE_ASSUME_SENT", "evidence_ref": "inc-1", "note": "Trying to bypass proof"},
		{"resolution": "CONFIRMED_FAILED", "reason_code": "MISSING_REF", "evidence_type": "MTA_REJECTED", "evidence_ref": "", "note": "No external ref supplied"},
		{"resolution": "CANCEL", "reason_code": "bad code!", "evidence_type": "PROVIDER_NOT_SUBMITTED", "evidence_ref": "prov-99", "note": "Invalid code format"},
		{"resolution": "CONFIRMED_SENT", "reason_code": "PII_IN_NOTE", "evidence_type": "MTA_ACCEPTED", "evidence_ref": "msg-22", "note": "Accepted for guest@example.test ok"},
		{"resolution": "CONFIRMED_SENT", "reason_code": "PHONE_IN_REF", "evidence_type": "MTA_ACCEPTED", "evidence_ref": "+44 7700 900123", "note": "Valid short note here"},
	}
	for i, bp := range badPayloads {
		b, _ := json.Marshal(bp)
		r := authedJSON(t, mgrClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/outbox/%s/reconcile", srv.URL, prop.ID, outUnkAPI), mgrCSRF, b)
		r.Body.Close()
		if r.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("badPayload[%d]: expected 422 validation error, got %d", i, r.StatusCode)
		}
	}

	// 4. Valid evidence-backed WebUI reconcile as RoleManager settles outUnkUI cleanly & records structured metadata + audit log!
	mgrClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	mgrForm := url.Values{
		"csrf_token": {mgrCSRF}, "resolution": {"CANCEL"}, "reason_code": {"PROVIDER_NO_SUBMISSION"},
		"evidence_type": {"PROVIDER_NOT_SUBMITTED"}, "evidence_ref": {"webhook-log-901"}, "note": {"SMTP log confirmed connection dropped pre-DATA"},
	}
	mgrUIResp, err := mgrClient.PostForm(fmt.Sprintf("%s/communications/outbox/%s/reconcile", srv.URL, outUnkUI), mgrForm)
	if err != nil {
		t.Fatalf("manager WebUI reconcile: %v", err)
	}
	mgrUIResp.Body.Close()
	if mgrUIResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect from WebUI reconcile, got %d", mgrUIResp.StatusCode)
	}

	var recBy uuid.UUID
	var recDecision, recEvType, recEvRef, recReasonCode, recStatus string
	if err := store.Pool().QueryRow(ctx, `
		SELECT status, reconciled_by, reconciled_decision, reconciled_evidence_type, reconciled_evidence_ref, reconciled_reason_code
		FROM email_outbox WHERE id = $1
	`, outUnkUI).Scan(&recStatus, &recBy, &recDecision, &recEvType, &recEvRef, &recReasonCode); err != nil {
		t.Fatalf("query reconciled outbox: %v", err)
	}
	if recStatus != domain.OutboxStatusCancelled || recBy != mgrUser.ID || recDecision != "CANCEL" ||
		recEvType != "PROVIDER_NOT_SUBMITTED" || recEvRef != "webhook-log-901" || recReasonCode != "PROVIDER_NO_SUBMISSION" {
		t.Fatalf("unexpected reconciled row state: status=%s by=%s dec=%s ev=%s/%s code=%s", recStatus, recBy, recDecision, recEvType, recEvRef, recReasonCode)
	}

	var auditCount int
	_ = store.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs_effective
		WHERE property_id = $1 AND action = 'email_outbox.reconciled' AND resource_id = $2
	`, prop.ID, outUnkUI.String()).Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("expected 1 email_outbox.reconciled audit record, got %d", auditCount)
	}

	// 5. Inactive manager MUST fail reconciliation cleanly (401/403).
	if _, err := store.Pool().Exec(ctx, `UPDATE admin_users SET is_active = FALSE WHERE id = $1`, mgrUser.ID); err != nil {
		t.Fatalf("deactivate manager: %v", err)
	}
	inactResp := authedJSON(t, mgrClient, http.MethodPost, fmt.Sprintf("%s/api/v1/properties/%s/outbox/%s/reconcile", srv.URL, prop.ID, outUnkAPI), mgrCSRF, validPayload)
	inactResp.Body.Close()
	if inactResp.StatusCode != http.StatusUnauthorized && inactResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected inactive actor reconcile rejected 401/403, got %d", inactResp.StatusCode)
	}

	// 6. RedactHistoricalAuditLog PII-smuggling rejection via service layer.
	var anyAuditID int64
	_ = store.Pool().QueryRow(ctx, `SELECT id FROM audit_logs_effective WHERE property_id = $1 ORDER BY id DESC LIMIT 1`, prop.ID).Scan(&anyAuditID)
	if _, err := svc.RedactHistoricalAuditLog(ctx, ownerActor, prop.ID, anyAuditID, "Erase leak@example.com from logs"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected PII email in RedactHistoricalAuditLog reason to fail ErrValidation, got %v", err)
	}
}

func assertSignoffPGCode(t *testing.T, err error, want, label string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != want {
		t.Fatalf("%s: expected PG SQLSTATE %s, got err=%v", label, want, err)
	}
}

func openSignoffAppRoleURL(t *testing.T, store *postgres.Store) string {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	dbName := strings.TrimPrefix(parsed.Path, "/")
	grantSQL := fmt.Sprintf(`
		GRANT CONNECT ON DATABASE %q TO lovelace_app;
		GRANT USAGE ON SCHEMA public TO lovelace_app;
		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO lovelace_app;
		GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO lovelace_app;
		GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO lovelace_app;
		REVOKE ALL ON TABLE public.audit_logs FROM PUBLIC, lovelace_app;
		GRANT INSERT ON TABLE public.audit_logs TO lovelace_app;
		GRANT USAGE, SELECT ON SEQUENCE public.audit_logs_id_seq TO lovelace_app;
		GRANT SELECT ON TABLE public.audit_logs_effective TO lovelace_app;
		REVOKE ALL ON TABLE public.audit_log_redactions FROM PUBLIC, lovelace_app;
		REVOKE ALL ON TABLE public.guest_consent_redactions FROM PUBLIC, lovelace_app;
		REVOKE ALL ON TABLE public.guest_consent_records FROM PUBLIC, lovelace_app;
		GRANT SELECT, INSERT ON TABLE public.guest_consent_records TO lovelace_app;
		GRANT UPDATE (guest_id) ON TABLE public.guest_consent_records TO lovelace_app;
		GRANT SELECT ON TABLE public.guest_consent_records_effective TO lovelace_app;
		REVOKE ALL ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) FROM PUBLIC;
		GRANT EXECUTE ON FUNCTION public.redact_audit_log_entry(UUID, BIGINT, JSONB, JSONB, JSONB, TEXT, UUID) TO lovelace_app;
		REVOKE ALL ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) FROM PUBLIC;
		GRANT EXECUTE ON FUNCTION public.redact_guest_consent_note(UUID, UUID, TEXT, TEXT, UUID) TO lovelace_app;
	`, dbName)
	if _, err := store.Pool().Exec(ctx, grantSQL); err != nil {
		t.Fatalf("grant lovelace_app permissions: %v", err)
	}
	appURL := os.Getenv("TEST_APP_DATABASE_URL")
	if appURL == "" {
		appParsed := *parsed
		appParsed.User = url.UserPassword("lovelace_app", "lovelace_app_dev_pw")
		appURL = appParsed.String()
	}
	return appURL
}

func TestPhase332SignoffDispatchLeaseExpiryAndClockAuthority(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-signoff-lease")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	queueForTest := func(email string) (domain.Guest, uuid.UUID) {
		g, err := svc.CreateGuest(ctx, ownerActor, domain.Guest{
			PropertyID: prop.ID, FirstName: "Lease", LastName: "Signoff", Email: email,
		})
		if err != nil {
			t.Fatalf("create guest %s: %v", email, err)
		}
		comm, err := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
			PropertyID: prop.ID, GuestID: g.ID, Recipient: g.Email,
			Subject: "Lease Verification", BodyText: "Testing strict lease expiry.",
		})
		if err != nil || comm.EmailOutboxID == nil {
			t.Fatalf("queue email for %s: comm=%+v err=%v", email, comm, err)
		}
		return g, *comm.EmailOutboxID
	}

	// 1. Valid active renewal succeeds, whereas expired lease CANNOT be renewed BEFORE sweeper runs.
	_, outExp := queueForTest("lease.expired.pre.sweep@example.test")
	claimedExp, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 5)
	if err != nil || len(claimedExp) != 1 || claimedExp[0].ID != outExp {
		t.Fatalf("claim outExp: %+v err=%v", claimedExp, err)
	}
	allowedExp, epochExp, err := store.BeginOutboxDispatchLease(ctx, outExp, claimedExp[0].WorkerToken, "worker-exp", clk.Now().UTC(), 60*time.Second)
	if err != nil || !allowedExp || epochExp < 1 {
		t.Fatalf("begin outExp lease: allowed=%v epoch=%d err=%v", allowedExp, epochExp, err)
	}
	clk.Advance(30 * time.Second)
	okRenew, err := store.RenewOutboxDispatchHeartbeat(ctx, outExp, claimedExp[0].WorkerToken, "worker-exp", epochExp, clk.Now().UTC(), 60*time.Second)
	if err != nil || !okRenew {
		t.Fatalf("expected active renewal to succeed, got ok=%v err=%v", okRenew, err)
	}
	var expiryBeforeExpiredAttempt time.Time
	_ = store.Pool().QueryRow(ctx, `SELECT dispatch_lease_expires_at FROM email_outbox WHERE id = $1`, outExp).Scan(&expiryBeforeExpiredAttempt)

	// Advance PAST expiry (60s) WITHOUT running SweepStaleOutboxDispatches yet.
	clk.Advance(65 * time.Second)
	lateRenew, err := store.RenewOutboxDispatchHeartbeat(ctx, outExp, claimedExp[0].WorkerToken, "worker-exp", epochExp, clk.Now().UTC(), 60*time.Second)
	if err != nil || lateRenew {
		t.Fatalf("expected expired lease renewal BEFORE sweeper to return false, got renewed=%v err=%v", lateRenew, err)
	}
	var expiryAfterExpiredAttempt time.Time
	_ = store.Pool().QueryRow(ctx, `SELECT dispatch_lease_expires_at FROM email_outbox WHERE id = $1`, outExp).Scan(&expiryAfterExpiredAttempt)
	if !expiryAfterExpiredAttempt.Equal(expiryBeforeExpiredAttempt) {
		t.Fatalf("expired renewal mutated dispatch_lease_expires_at: %s -> %s", expiryBeforeExpiredAttempt, expiryAfterExpiredAttempt)
	}

	// Second worker CANNOT claim or duplicate while DISPATCHING (nor after sweep moves to DISPATCH_UNKNOWN).
	dupClaim, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(dupClaim) != 0 {
		t.Fatalf("expected 0 duplicate claims while row is DISPATCHING, got %d err=%v", len(dupClaim), err)
	}

	// Expired worker BEFORE sweeper CANNOT complete, mark unknown, abort pre-send, retry, or fail the row.
	if err := store.MarkOutboxEmailSentWithLeaseAt(ctx, outExp, claimedExp[0].WorkerToken, epochExp, clk.Now().UTC(), clk.Now().UTC()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected expired worker MarkOutboxEmailSentWithLeaseAt -> ErrConflict, got %v", err)
	}
	if err := store.MarkOutboxDispatchUnknownWithLeaseAt(ctx, outExp, claimedExp[0].WorkerToken, epochExp, "late", clk.Now().UTC(), clk.Now().UTC()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected expired worker MarkOutboxDispatchUnknownWithLeaseAt -> ErrConflict, got %v", err)
	}
	if err := store.MarkOutboxDispatchTerminatedBeforeSendWithLeaseAt(ctx, outExp, claimedExp[0].WorkerToken, epochExp, "late", clk.Now().UTC().Add(time.Minute), clk.Now().UTC(), clk.Now().UTC()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected expired worker MarkOutboxDispatchTerminatedBeforeSendWithLeaseAt -> ErrConflict, got %v", err)
	}
	if err := store.MarkOutboxEmailRetryWithLeaseAt(ctx, outExp, claimedExp[0].WorkerToken, epochExp, "late", clk.Now().UTC().Add(time.Minute), clk.Now().UTC()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected expired worker MarkOutboxEmailRetryWithLeaseAt -> ErrConflict, got %v", err)
	}
	if err := store.MarkOutboxEmailPermanentFailureWithLeaseAt(ctx, outExp, claimedExp[0].WorkerToken, epochExp, "late", clk.Now().UTC()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected expired worker MarkOutboxEmailPermanentFailureWithLeaseAt -> ErrConflict, got %v", err)
	}

	// Now sweeper fences the expired lease -> epoch advances monotonically & still cannot be renewed or settled.
	fenced, err := store.SweepStaleOutboxDispatches(ctx, clk.Now().UTC())
	if err != nil || len(fenced) != 1 || fenced[0] != outExp {
		t.Fatalf("expected sweep to fence outExp, got %v err=%v", fenced, err)
	}
	var stAfterSweep string
	var epochAfterSweep int64
	_ = store.Pool().QueryRow(ctx, `SELECT status, dispatch_fence_epoch FROM email_outbox WHERE id = $1`, outExp).Scan(&stAfterSweep, &epochAfterSweep)
	if stAfterSweep != domain.OutboxStatusDispatchUnknown || epochAfterSweep != epochExp+1 {
		t.Fatalf("expected DISPATCH_UNKNOWN epoch=%d, got status=%s epoch=%d", epochExp+1, stAfterSweep, epochAfterSweep)
	}
	postSweepRenew, err := store.RenewOutboxDispatchHeartbeat(ctx, outExp, claimedExp[0].WorkerToken, "worker-exp", epochExp, clk.Now().UTC(), 60*time.Second)
	if err != nil || postSweepRenew {
		t.Fatalf("expected renewal after sweeper to fail cleanly, got renewed=%v err=%v", postSweepRenew, err)
	}
	dupAfterSweep, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 10)
	if err != nil || len(dupAfterSweep) != 0 {
		t.Fatalf("expected 0 claims when item is DISPATCH_UNKNOWN, got %d err=%v", len(dupAfterSweep), err)
	}

	// 2. Concurrent RenewOutboxDispatchHeartbeat vs SweepStaleOutboxDispatches race serializes safely on row lock.
	_, outRace := queueForTest("lease.concurrent.race@example.test")
	claimedRace, err := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 5)
	if err != nil || len(claimedRace) != 1 {
		t.Fatalf("claim outRace: %+v err=%v", claimedRace, err)
	}
	allowedRace, epochRace, err := store.BeginOutboxDispatchLease(ctx, outRace, claimedRace[0].WorkerToken, "worker-race", clk.Now().UTC(), 60*time.Second)
	if err != nil || !allowedRace {
		t.Fatalf("begin outRace lease: allowed=%v err=%v", allowedRace, err)
	}
	clk.Advance(60 * time.Second) // exact boundary: sweep checks <= clock, renew checks > clock
	var raceWG sync.WaitGroup
	var raceRenewed bool
	var raceFenced []uuid.UUID
	raceWG.Add(2)
	go func() {
		defer raceWG.Done()
		raceRenewed, _ = store.RenewOutboxDispatchHeartbeat(ctx, outRace, claimedRace[0].WorkerToken, "worker-race", epochRace, clk.Now().UTC().Add(-time.Second), 60*time.Second)
	}()
	go func() {
		defer raceWG.Done()
		raceFenced, _ = store.SweepStaleOutboxDispatches(ctx, clk.Now().UTC())
	}()
	raceWG.Wait()
	// At most one side wins the fence vs renew transition depending on lock arrival order, and row state is consistent.
	var raceStatus string
	var raceEpochAfter int64
	_ = store.Pool().QueryRow(ctx, `SELECT status, dispatch_fence_epoch FROM email_outbox WHERE id = $1`, outRace).Scan(&raceStatus, &raceEpochAfter)
	if raceStatus == domain.OutboxStatusDispatchUnknown {
		// Sweeper fenced it -> epoch advanced by 1 and worker can no longer settle.
		if raceEpochAfter != epochRace+1 {
			t.Fatalf("expected epoch=%d on DISPATCH_UNKNOWN, got %d", epochRace+1, raceEpochAfter)
		}
		if err := store.MarkOutboxEmailSentWithLeaseAt(ctx, outRace, claimedRace[0].WorkerToken, epochRace, clk.Now().UTC(), clk.Now().UTC()); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("expected fenced worker settlement -> ErrConflict, got %v", err)
		}
	} else if raceStatus == domain.OutboxStatusDispatching {
		if !raceRenewed || len(raceFenced) != 0 {
			t.Fatalf("if DISPATCHING survived sweep, expected renewed=true & fenced=0, got renewed=%v fenced=%v", raceRenewed, raceFenced)
		}
	} else {
		t.Fatalf("unexpected status after race: %s", raceStatus)
	}

	// 3. Single PG clock_timestamp() authority (nowUTC = zero time.Time{}): lease created, renewed, expired, and blocked cleanly by DB clock.
	svc.SetClock(time.Now)
	_, outPGClk := queueForTest("lease.pgclock@example.test")
	claimedPG, err := store.ClaimDueOutboxEmails(ctx, time.Now().UTC().Add(time.Second), 5)
	if err != nil || len(claimedPG) != 1 || claimedPG[0].ID != outPGClk {
		t.Fatalf("claim outPGClk: %+v err=%v", claimedPG, err)
	}
	okPG, epochPG, err := store.BeginOutboxDispatchLease(ctx, outPGClk, claimedPG[0].WorkerToken, "worker-pgclk", time.Time{}, 30*time.Millisecond)
	if err != nil || !okPG {
		t.Fatalf("begin PG clock lease: ok=%v err=%v", okPG, err)
	}
	// Poll PG clock until clock_timestamp() > dispatch_lease_expires_at cleanly without fragile sleeps.
	deadlinePG := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadlinePG) {
		var expired bool
		_ = store.Pool().QueryRow(ctx, `SELECT clock_timestamp() > dispatch_lease_expires_at FROM email_outbox WHERE id = $1`, outPGClk).Scan(&expired)
		if expired {
			break
		}
	}
	pgRenewAfterExp, err := store.RenewOutboxDispatchHeartbeat(ctx, outPGClk, claimedPG[0].WorkerToken, "worker-pgclk", epochPG, time.Time{}, 60*time.Second)
	if err != nil || pgRenewAfterExp {
		t.Fatalf("expected zero-clock RenewOutboxDispatchHeartbeat to reject expired DB clock lease, got renewed=%v err=%v", pgRenewAfterExp, err)
	}
	if err := store.MarkOutboxEmailSentWithLeaseAt(ctx, outPGClk, claimedPG[0].WorkerToken, epochPG, time.Time{}, time.Time{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected zero-clock MarkOutboxEmailSentWithLeaseAt on expired lease -> ErrConflict, got %v", err)
	}
}

func TestPhase332SignoffHeartbeatAbortTimingAndLocalLocking(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p332-signoff-hb")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	// 1. Validate canonical DefaultDispatchConfig and reject unsafe timing relationships.
	def := service.DefaultDispatchConfig()
	if def.LeaseTTL != 60*time.Second || def.HeartbeatInterval != 20*time.Second || def.TransportTimeout != 30*time.Second {
		t.Fatalf("unexpected DefaultDispatchConfig: %+v", def)
	}
	for _, badCfg := range []service.DispatchConfig{
		{LeaseTTL: 60 * time.Second, HeartbeatInterval: 45 * time.Second, TransportTimeout: 30 * time.Second},
		{LeaseTTL: 60 * time.Second, HeartbeatInterval: 20 * time.Second, TransportTimeout: 60 * time.Second},
		{LeaseTTL: 60 * time.Second, HeartbeatInterval: 20 * time.Second, TransportTimeout: 2 * time.Minute},
		{LeaseTTL: 5 * time.Millisecond, HeartbeatInterval: 2 * time.Millisecond, TransportTimeout: 3 * time.Millisecond},
	} {
		if err := svc.SetDispatchConfig(badCfg); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("expected SetDispatchConfig(%+v) -> ErrValidation, got %v", badCfg, err)
		}
	}

	// 2. Oversized SMTPSender.Timeout is bounded by DispatchPendingEmails TransportTimeout (< LeaseTTL).
	lnCap, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen lnCap: %v", err)
	}
	defer lnCap.Close()
	capEntered := make(chan struct{})
	capClosed := make(chan struct{})
	go func() {
		conn, err := lnCap.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(capClosed)
		_, _ = conn.Write([]byte("220 localhost ESMTP\r\n"))
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			upper := strings.ToUpper(line)
			if strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO") {
				_, _ = conn.Write([]byte("250-localhost\r\n250 OK\r\n"))
			} else if strings.HasPrefix(upper, "MAIL FROM:") {
				close(capEntered)
				// Read until client closes due to bounded TransportTimeout (80ms) rather than 5m SMTPSender.Timeout.
				for {
					if _, rErr := r.ReadString('\n'); rErr != nil {
						return
					}
				}
			}
		}
	}()
	gCap, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Cap", LastName: "Timeout", Email: "cap.timeout@example.test"})
	commCap, _ := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gCap.ID, Recipient: gCap.Email, Subject: "Cap Test", BodyText: "Body",
	})
	svc.SetClock(time.Now)
	if err := svc.SetDispatchConfig(service.DispatchConfig{
		WorkerID: "cap-worker", LeaseTTL: 200 * time.Millisecond, HeartbeatInterval: 60 * time.Millisecond, TransportTimeout: 80 * time.Millisecond,
	}); err != nil {
		t.Fatalf("SetDispatchConfig cap: %v", err)
	}
	svc.SetEmailSender(&mailer.SMTPSender{Addr: lnCap.Addr().String(), Timeout: 5 * time.Minute})
	startCap := time.Now()
	_, _ = svc.DispatchPendingEmails(ctx)
	<-capEntered
	<-capClosed
	if elapsed := time.Since(startCap); elapsed > time.Second {
		t.Fatalf("expected oversized SMTPSender.Timeout to be capped near 80ms, took %s", elapsed)
	}
	var stCap, outcCap string
	_ = store.Pool().QueryRow(ctx, `SELECT status, dispatch_outcome FROM email_outbox WHERE id = $1`, *commCap.EmailOutboxID).Scan(&stCap, &outcCap)
	if stCap != domain.OutboxStatusPending {
		t.Fatalf("expected pre-DATA capped timeout -> PENDING retry, got status=%s outcome=%s", stCap, outcCap)
	}
	_, _ = store.Pool().Exec(ctx, `UPDATE email_outbox SET status = 'CANCELLED', cancelled_at = NOW(), recipient_email = '[redacted]', subject = '[redacted]', body_text = '[redacted]' WHERE id = $1`, *commCap.EmailOutboxID)

	// 3. Heartbeat DB error AND definitive lease loss both immediately stop blocked SMTPSender (pre-DATA and post-DATA) + keep local active lock until PG settles.
	appURL := openSignoffAppRoleURL(t, store)
	workerStore, err := postgres.New(ctx, appURL)
	if err != nil {
		t.Fatalf("open lovelace_app workerStore: %v", err)
	}
	defer workerStore.Close()
	t.Cleanup(func() {
		_, _ = store.Pool().Exec(context.Background(), `GRANT UPDATE ON TABLE public.email_outbox TO lovelace_app`)
	})

	workerSvc := service.New(workerStore, config.Config{EmailFrom: "bookings@tirbach.wales"})
	workerSvc.SetClock(time.Now)
	if err := workerSvc.SetDispatchConfig(service.DispatchConfig{
		WorkerID: "hb-abort-worker", LeaseTTL: 4 * time.Second, HeartbeatInterval: 40 * time.Millisecond, TransportTimeout: 3 * time.Second,
	}); err != nil {
		t.Fatalf("SetDispatchConfig workerSvc: %v", err)
	}

	// 3a. Pre-DATA heartbeat DB error (REVOKE UPDATE (dispatch_heartbeat_at)) cancels SMTP transport immediately & classifies PENDING.
	lnPre, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen lnPre: %v", err)
	}
	defer lnPre.Close()
	preStalled := make(chan struct{})
	preClosed := make(chan struct{})
	go func() {
		conn, err := lnPre.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(preClosed)
		_, _ = conn.Write([]byte("220 localhost ESMTP\r\n"))
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			upper := strings.ToUpper(line)
			if strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO") {
				_, _ = conn.Write([]byte("250-localhost\r\n250 OK\r\n"))
			} else if strings.HasPrefix(upper, "MAIL FROM:") {
				close(preStalled)
				for {
					if _, rErr := r.ReadString('\n'); rErr != nil {
						return
					}
				}
			}
		}
	}()
	gPreHB, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Pre", LastName: "HBErr", Email: "pre.hberr@example.test"})
	commPreHB, _ := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gPreHB.ID, Recipient: gPreHB.Email, Subject: "Pre-DATA HB Err", BodyText: "Body",
	})
	outPreHB := *commPreHB.EmailOutboxID
	workerSvc.SetEmailSender(&mailer.SMTPSender{Addr: lnPre.Addr().String(), Timeout: 3 * time.Second})
	preDone := make(chan struct{})
	go func() {
		_, _ = workerSvc.DispatchPendingEmails(ctx)
		close(preDone)
	}()
	<-preStalled
	// Revoke ONLY dispatch_heartbeat_at update -> RenewOutboxDispatchHeartbeat hits SQLSTATE 42501 -> must cancel sendCtx immediately!
	const revokeHeartbeatOnlySQL = `
		REVOKE UPDATE ON TABLE public.email_outbox FROM lovelace_app;
		GRANT UPDATE (
			status, attempts, last_error, next_attempt_at, locked_at, worker_token,
			dispatched_at, dispatch_lease_expires_at, dispatch_fence_epoch, dispatch_fenced_at, dispatch_outcome,
			cancel_reason, cancelled_at, recipient_email, subject, body_text, body_html, sent_at,
			communication_id, guest_id, updated_at, dispatch_worker_id,
			reconciled_by, reconciled_at, reconciled_decision, reconciled_evidence_type, reconciled_evidence_ref, reconciled_reason_code
		) ON TABLE public.email_outbox TO lovelace_app;
	`
	if _, err := store.Pool().Exec(ctx, revokeHeartbeatOnlySQL); err != nil {
		t.Fatalf("revoke dispatch_heartbeat_at: %v", err)
	}
	select {
	case <-preClosed:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("pre-DATA SMTPSender was NOT cancelled after heartbeat DB error")
	}
	<-preDone
	_, _ = store.Pool().Exec(ctx, `GRANT UPDATE ON TABLE public.email_outbox TO lovelace_app`)
	var stPreHB string
	_ = store.Pool().QueryRow(ctx, `SELECT status FROM email_outbox WHERE id = $1`, outPreHB).Scan(&stPreHB)
	if stPreHB != domain.OutboxStatusPending {
		t.Fatalf("expected pre-DATA heartbeat DB abort to settle safely as PENDING retry, got %s", stPreHB)
	}
	_, _ = store.Pool().Exec(ctx, `UPDATE email_outbox SET status = 'CANCELLED', cancelled_at = NOW(), recipient_email = '[redacted]', subject = '[redacted]', body_text = '[redacted]' WHERE id = $1`, outPreHB)

	// 3b. Post-DATA heartbeat DB error cancels SMTP transport immediately & classifies DISPATCH_UNKNOWN (never FAILED/CANCELLED).
	lnPost, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen lnPost: %v", err)
	}
	defer lnPost.Close()
	postDotStalled := make(chan struct{})
	postClosed := make(chan struct{})
	go func() {
		conn, err := lnPost.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(postClosed)
		_, _ = conn.Write([]byte("220 localhost ESMTP\r\n"))
		r := bufio.NewReader(conn)
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					close(postDotStalled)
					for {
						if _, rErr := r.ReadString('\n'); rErr != nil {
							return
						}
					}
				}
				continue
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
				_, _ = conn.Write([]byte("250-localhost\r\n250 OK\r\n"))
			case strings.HasPrefix(upper, "MAIL FROM:") || strings.HasPrefix(upper, "RCPT TO:"):
				_, _ = conn.Write([]byte("250 OK\r\n"))
			case strings.HasPrefix(upper, "DATA"):
				inData = true
				_, _ = conn.Write([]byte("354 End data\r\n"))
			}
		}
	}()
	gPostHB, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Post", LastName: "HBErr", Email: "post.hberr@example.test"})
	commPostHB, _ := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gPostHB.ID, Recipient: gPostHB.Email, Subject: "Post-DATA HB Err", BodyText: "Body",
	})
	outPostHB := *commPostHB.EmailOutboxID
	workerSvc.SetEmailSender(&mailer.SMTPSender{Addr: lnPost.Addr().String(), Timeout: 3 * time.Second})
	postDone := make(chan struct{})
	go func() {
		_, _ = workerSvc.DispatchPendingEmails(ctx)
		close(postDone)
	}()
	<-postDotStalled
	if _, err := store.Pool().Exec(ctx, revokeHeartbeatOnlySQL); err != nil {
		t.Fatalf("revoke dispatch_heartbeat_at post-DATA: %v", err)
	}
	select {
	case <-postClosed:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("post-DATA SMTPSender was NOT cancelled after heartbeat DB error")
	}
	<-postDone
	_, _ = store.Pool().Exec(ctx, `GRANT UPDATE ON TABLE public.email_outbox TO lovelace_app`)
	var stPostHB, outcPostHB string
	_ = store.Pool().QueryRow(ctx, `SELECT status, dispatch_outcome FROM email_outbox WHERE id = $1`, outPostHB).Scan(&stPostHB, &outcPostHB)
	if stPostHB != domain.OutboxStatusDispatchUnknown || outcPostHB != "UNCERTAIN_TIMEOUT" {
		t.Fatalf("expected post-DATA heartbeat abort -> DISPATCH_UNKNOWN / UNCERTAIN_TIMEOUT, got status=%s outcome=%s", stPostHB, outcPostHB)
	}

	// 4. Local active dispatch barrier blocks ReconcileUnknownOutboxDispatch until worker goroutine completes & unregisters.
	gLocal, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{PropertyID: prop.ID, FirstName: "Local", LastName: "Active", Email: "local.active@example.test"})
	commLocal, _ := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: gLocal.ID, Recipient: gLocal.Email, Subject: "Local Barrier", BodyText: "Body",
	})
	outLocal := *commLocal.EmailOutboxID
	if err := svc.SetDispatchConfig(service.DispatchConfig{
		WorkerID: "local-worker", LeaseTTL: 2 * time.Second, HeartbeatInterval: 500 * time.Millisecond, TransportTimeout: 1500 * time.Millisecond,
	}); err != nil {
		t.Fatalf("SetDispatchConfig localBarrier: %v", err)
	}
	localMailer := newBarrierMailer()
	localMailer.ignoreCtx = true
	svc.SetEmailSender(localMailer)
	localDone := make(chan struct{})
	go func() {
		_, _ = svc.DispatchPendingEmails(ctx)
		close(localDone)
	}()
	<-localMailer.started
	// Force DB row into DISPATCH_UNKNOWN artificially while local worker is STILL active inside Send().
	_, _ = store.Pool().Exec(ctx, `
		UPDATE email_outbox
		SET status = 'DISPATCH_UNKNOWN', dispatch_fenced_at = NOW(), dispatch_lease_expires_at = NULL, dispatch_outcome = 'WORKER_FENCED'
		WHERE id = $1
	`, outLocal)
	if _, err := svc.ReconcileUnknownOutboxDispatch(ctx, ownerActor, service.ReconcileOutboxInput{
		PropertyID: prop.ID, OutboxID: outLocal, Resolution: "CONFIRMED_SENT",
		ReasonCode: "CONSERVATIVE", EvidenceType: "CONSERVATIVE_ASSUME_SENT", EvidenceRef: "local-check-1", Note: "Should block while active locally",
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict reconciling while worker locally active, got %v", err)
	}
	localMailer.release <- context.DeadlineExceeded
	<-localDone
	if _, err := svc.ReconcileUnknownOutboxDispatch(ctx, ownerActor, service.ReconcileOutboxInput{
		PropertyID: prop.ID, OutboxID: outLocal, Resolution: "CONFIRMED_SENT",
		ReasonCode: "CONSERVATIVE", EvidenceType: "CONSERVATIVE_ASSUME_SENT", EvidenceRef: "local-check-2", Note: "Succeeds cleanly after worker exits",
	}); err != nil {
		t.Fatalf("expected ReconcileUnknownOutboxDispatch to succeed after local worker exited, got %v", err)
	}
}

func TestPhase332SignoffDBGuardsAndOperatorRotation(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-signoff-db")
	defer srv.Close()

	ctx := context.Background()
	owner, _ := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}
	rotMgr, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &prop.ID, Email: "rot.mgr@example.test", FullName: "Rotating Manager",
		Role: domain.RoleManager, Password: "rotating-manager-password-2026",
	})
	if err != nil {
		t.Fatalf("create rotMgr: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE admin_users SET must_rotate_password = TRUE WHERE id = $1`, rotMgr.ID); err != nil {
		t.Fatalf("mark rotMgr must_rotate_password=true: %v", err)
	}

	g, _ := svc.CreateGuest(ctx, ownerActor, domain.Guest{
		PropertyID: prop.ID, FirstName: "Guard", LastName: "Target", Email: "guard.target@example.test", Phone: "+44 7700 900123",
	})
	consent, err := svc.RecordGuestConsent(ctx, ownerActor, domain.GuestConsentRecord{
		PropertyID: prop.ID, GuestID: g.ID, ConsentType: "MARKETING_EMAIL", Status: domain.ConsentGranted,
		Method: "ADMIN_MANUAL", Source: "ADMIN_PANEL", Notes: "Legacy note prior to erasure",
	})
	if err != nil {
		t.Fatalf("record consent: %v", err)
	}
	var auditID int64
	if err := store.Pool().QueryRow(ctx, `
		INSERT INTO audit_logs (property_id, actor_email, actor_role, action, resource_type, resource_id, request_id, before_state, after_state, metadata)
		VALUES ($1, 'legacy@raw.test', 'PUBLIC', 'reservation.updated', 'reservation', $2, 'req-signoff',
		        '{"guest_name":"Raw Legacy","guest_email":"legacy@raw.test"}'::jsonb,
		        '{"phone":"07700 900123"}'::jsonb,
		        '{"notes":"Private raw free text"}'::jsonb)
		RETURNING id
	`, prop.ID, g.ID.String()).Scan(&auditID); err != nil {
		t.Fatalf("seed legacy audit row: %v", err)
	}

	appURL := openSignoffAppRoleURL(t, store)
	appPool, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("connect lovelace_app: %v", err)
	}
	defer appPool.Close()

	// 1. Both redact_guest_consent_note and redact_audit_log_entry reject email, UK/intl phone numbers, and control/multiline chars (SQLSTATE 22023).
	badReasons := []struct {
		name   string
		reason string
	}{
		{"email", "Contacted jane.doe@example.test for erasure"},
		{"uk international phone", "Confirmed by phone +44 7700 900123"},
		{"uk domestic mobile spaced", "Guest called from 07700 900123 today"},
		{"uk domestic mobile compact", "Guest called from 07700900123 today"},
		{"uk domestic landline", "Site office call 01437 555123 received"},
		{"multiline", "Line one reason\nLine two smuggled"},
	}
	for _, tc := range badReasons {
		var rid uuid.UUID
		cErr := appPool.QueryRow(ctx, `SELECT public.redact_guest_consent_note($1, $2, '[redacted]', $3, $4)`,
			uuid.New(), consent.ID, tc.reason, owner.ID).Scan(&rid)
		assertSignoffPGCode(t, cErr, "22023", "consent reason "+tc.name)

		aErr := appPool.QueryRow(ctx, `SELECT public.redact_audit_log_entry($1, $2, NULL, NULL, '{"redacted":true}'::jsonb, $3, $4)`,
			uuid.New(), auditID, tc.reason, owner.ID).Scan(&rid)
		assertSignoffPGCode(t, aErr, "22023", "audit reason "+tc.name)
	}

	// 2. redact_audit_log_entry rejects PII/credential/free-text keys and nested/array phone+email smuggling (SQLSTATE 22023).
	badJSONs := []struct {
		name string
		json string
	}{
		{"pii key guest_email", `{"guest_email":"hidden@example.test"}`},
		{"nested pii key phone", `{"outer":{"phone":"+447700900123"}}`},
		{"pii key notes", `{"notes":"Free text note"}`},
		{"secret key password_hash", `{"password_hash":"$2a$10$abc"}`},
		{"ip_address key", `{"ip_address":"192.0.2.10"}`},
		{"email inside structural string value", `{"status":"contact admin@example.test"}`},
		{"uk phone inside structural string value", `{"reference":"tel 07700 900123"}`},
		{"phone inside JSON array", `{"tags":["safe","07700900123"]}`},
		{"email inside nested JSON array object", `{"items":[{"label":"alice@example.test"}]}`},
	}
	for _, bj := range badJSONs {
		var rid uuid.UUID
		bErr := appPool.QueryRow(ctx, `SELECT public.redact_audit_log_entry($1, $2, $3::jsonb, NULL, '{"redacted":true}'::jsonb, 'Authorized historical PII cleanup', $4)`,
			uuid.New(), auditID, bj.json, owner.ID).Scan(&rid)
		assertSignoffPGCode(t, bErr, "22023", "before_state "+bj.name)

		aErr := appPool.QueryRow(ctx, `SELECT public.redact_audit_log_entry($1, $2, NULL, $3::jsonb, '{"redacted":true}'::jsonb, 'Authorized historical PII cleanup', $4)`,
			uuid.New(), auditID, bj.json, owner.ID).Scan(&rid)
		assertSignoffPGCode(t, aErr, "22023", "after_state "+bj.name)

		mErr := appPool.QueryRow(ctx, `SELECT public.redact_audit_log_entry($1, $2, NULL, NULL, $3::jsonb, 'Authorized historical PII cleanup', $4)`,
			uuid.New(), auditID, bj.json, owner.ID).Scan(&rid)
		assertSignoffPGCode(t, mErr, "22023", "metadata "+bj.name)
	}

	// 3. Overlay table BEFORE INSERT triggers also block direct table inserts carrying phone/PII.
	_, trigConsentErr := store.Pool().Exec(ctx, `
		INSERT INTO guest_consent_redactions (id, consent_record_id, property_id, redacted_notes, reason, reason_category, redacted_by)
		VALUES ($1, $2, $3, '[redacted]', 'Phone 07700 900123 smuggled', 'GUEST_ERASURE', $4)
	`, uuid.New(), consent.ID, prop.ID, owner.ID)
	assertSignoffPGCode(t, trigConsentErr, "22023", "trigger guest_consent_redactions")

	_, trigAuditErr := store.Pool().Exec(ctx, `
		INSERT INTO audit_log_redactions (id, audit_log_id, property_id, redacted_before_state, reason, reason_category, redacted_by)
		VALUES ($1, $2, $3, '{"nested":{"email":"x@y.com"}}'::jsonb, 'Valid clean reason', 'HISTORICAL_PII_MASK', $4)
	`, uuid.New(), auditID, prop.ID, owner.ID)
	assertSignoffPGCode(t, trigAuditErr, "22023", "trigger audit_log_redactions")

	// 4. Legitimate RedactHistoricalAuditLog + direct minimized redaction succeed cleanly.
	redactedEntry, err := svc.RedactHistoricalAuditLog(ctx, ownerActor, prop.ID, auditID, "Phase 3.3.2 signoff historical redaction")
	if err != nil || !redactedEntry.IsRedacted {
		t.Fatalf("expected RedactHistoricalAuditLog to succeed with minimized snapshot, got entry=%+v err=%v", redactedEntry, err)
	}

	// 5. Operator with must_rotate_password = TRUE in DB MUST be denied ReconcileUnknownOutboxDispatch
	// even if caller supplies stale Actor{MustRotatePassword: false}.
	comm, _ := svc.SendGuestEmail(ctx, ownerActor, service.SendGuestEmailInput{
		PropertyID: prop.ID, GuestID: g.ID, Recipient: g.Email, Subject: "Rotation Test", BodyText: "Body",
	})
	outRotID := *comm.EmailOutboxID
	claimed, _ := store.ClaimDueOutboxEmails(ctx, clk.Now().UTC(), 5)
	_, _, _ = store.BeginOutboxDispatchLease(ctx, outRotID, claimed[0].WorkerToken, "rot-worker", clk.Now().UTC(), 60*time.Second)
	clk.Advance(90 * time.Second)
	_, _ = store.SweepStaleOutboxDispatches(ctx, clk.Now().UTC())

	staleRotActor := service.Actor{
		UserID: rotMgr.ID, PropertyID: &prop.ID, Email: rotMgr.Email,
		FullName: rotMgr.FullName, Role: domain.RoleManager, MustRotatePassword: false,
	}
	if _, err := svc.ReconcileUnknownOutboxDispatch(ctx, staleRotActor, service.ReconcileOutboxInput{
		PropertyID: prop.ID, OutboxID: outRotID, Resolution: "CONFIRMED_SENT",
		ReasonCode: "MTA_PROOF", EvidenceType: "MTA_ACCEPTED", EvidenceRef: "rot-mta-1", Note: "Should be denied due to DB must_rotate_password=true",
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected must_rotate_password=true operator to be denied ErrForbidden on reconcile, got %v", err)
	}
}
