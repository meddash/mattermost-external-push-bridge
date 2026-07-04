package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

type dispatcher struct {
	api      mmAPI
	store    eventStore
	metrics  *metrics
	getCfg   func() *runtimeConfig
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	queue    chan string
	queueCap int
}

func newDispatcher(api mmAPI, store eventStore, metrics *metrics, cfg *runtimeConfig) *dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &dispatcher{
		api:      api,
		store:    store,
		metrics:  metrics,
		getCfg:   func() *runtimeConfig { return cfg },
		ctx:      ctx,
		cancel:   cancel,
		queue:    make(chan string, cfg.QueueSize),
		queueCap: cfg.QueueSize,
	}
	for i := 0; i < cfg.WorkerCount; i++ {
		d.wg.Add(1)
		go d.worker(i + 1)
	}
	return d
}

func (d *dispatcher) updateConfigGetter(fn func() *runtimeConfig) {
	d.getCfg = fn
}

func (d *dispatcher) Enqueue(eventID string) bool {
	select {
	case d.queue <- eventID:
		return true
	default:
		d.metrics.queueDropped.Add(1)
		return false
	}
}

func (d *dispatcher) RecoverPending() error {
	eventIDs, err := d.store.ListRecoverable()
	if err != nil {
		return err
	}
	for _, eventID := range eventIDs {
		if !d.Enqueue(eventID) {
			d.api.LogWarn("Failed to enqueue recovered event", "event_id", eventID)
		}
	}
	return nil
}

func (d *dispatcher) Stop(timeout time.Duration) {
	d.cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		d.api.LogWarn("Timed out while waiting for dispatcher shutdown")
	}
}

func (d *dispatcher) QueueDepth() int {
	return len(d.queue)
}

func (d *dispatcher) worker(workerID int) {
	defer d.wg.Done()
	for {
		select {
		case <-d.ctx.Done():
			return
		case eventID := <-d.queue:
			d.processEvent(workerID, eventID)
		}
	}
}

func (d *dispatcher) processEvent(workerID int, eventID string) {
	record, err := d.store.Get(eventID)
	if err != nil {
		d.api.LogError("Failed to load outbox record", "event_id", eventID, "error", err.Error())
		return
	}
	if record == nil {
		return
	}

	if err := d.store.Update(eventID, func(record *outboxRecord) error {
		record.Status = eventStatusProcessing
		return nil
	}); err != nil {
		d.api.LogError("Failed to mark outbox record as processing", "event_id", eventID, "error", err.Error())
		return
	}

	cfg := d.getCfg()
	ctx, cancel := context.WithTimeout(d.ctx, cfg.RequestTimeout+2*time.Second)
	result := sendEvent(ctx, cfg, record.Event)
	cancel()

	if result.err == nil {
		d.metrics.delivered.Add(1)
		_ = d.store.Update(eventID, func(record *outboxRecord) error {
			record.Status = eventStatusDelivered
			record.LastError = ""
			record.LastHTTPStatus = result.httpStatus
			record.NextAttemptAt = 0
			return nil
		})
		d.api.LogInfo("External push event delivered",
			"event_id", eventID,
			"post_id", record.Event.Post.PostID,
			"recipient_user_id", record.Event.Recipient.UserID,
			"attempt", record.AttemptCount+1,
			"http_status", result.httpStatus,
			"worker_id", workerID,
		)
		return
	}

	attempt := record.AttemptCount + 1
	retryable := result.retryable && attempt <= cfg.MaxRetries
	if retryable {
		delay := computeBackoff(cfg, attempt, result.retryAfter)
		d.metrics.retries.Add(1)
		_ = d.store.Update(eventID, func(record *outboxRecord) error {
			record.Status = eventStatusPending
			record.AttemptCount = attempt
			record.LastError = result.errCategory
			record.LastHTTPStatus = result.httpStatus
			record.NextAttemptAt = time.Now().Add(delay).UnixMilli()
			return nil
		})
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-d.ctx.Done():
			return
		case <-timer.C:
			if !d.Enqueue(eventID) {
				d.api.LogWarn("Retry enqueue dropped", "event_id", eventID)
			}
		}
		d.api.LogWarn("External push event will be retried",
			"event_id", eventID,
			"post_id", record.Event.Post.PostID,
			"recipient_user_id", record.Event.Recipient.UserID,
			"attempt", attempt,
			"http_status", result.httpStatus,
			"error_type", result.errCategory,
		)
		return
	}

	d.metrics.permanentFailures.Add(1)
	_ = d.store.Update(eventID, func(record *outboxRecord) error {
		record.Status = eventStatusFailed
		record.AttemptCount = attempt
		record.LastError = result.errCategory
		record.LastHTTPStatus = result.httpStatus
		record.NextAttemptAt = 0
		return nil
	})
	d.api.LogError("External push event permanently failed",
		"event_id", eventID,
		"post_id", record.Event.Post.PostID,
		"recipient_user_id", record.Event.Recipient.UserID,
		"attempt", attempt,
		"http_status", result.httpStatus,
		"error_type", result.errCategory,
	)
}

func wrapAppErr(err *model.AppError) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w", err)
}
