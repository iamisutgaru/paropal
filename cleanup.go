package main

import (
	"context"
	"time"
)

func (a *app) runDailyCleanup(ctx context.Context) {
	now := time.Now()
	next := firstCleanupRunTimeKST(now, a.cleanupLoc)
	a.logger.Info("daily instance cleanup scheduler started",
		"timezone", cleanupTimeZone,
		"startup_kst", now.In(a.cleanupLoc).Format(time.RFC3339),
		"next_run_kst", next.In(a.cleanupLoc).Format(time.RFC3339),
	)

	for {
		wait := time.Until(next)
		if wait < 0 {
			wait = 0
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			a.logger.Info("daily instance cleanup scheduler stopped")
			return
		case <-timer.C:
			now := time.Now()
			windowStart, windowEnd := cleanupWindowBounds(now, a.cleanupLoc)
			if !isWithinCleanupWindow(now, a.cleanupLoc) {
				a.logger.Warn("skipping cleanup outside allowed window",
					"window_start_kst", windowStart.In(a.cleanupLoc).Format(time.RFC3339),
					"window_end_kst", windowEnd.In(a.cleanupLoc).Format(time.RFC3339),
					"current_kst", now.In(a.cleanupLoc).Format(time.RFC3339),
				)
				next = nextCleanupTimeKST(now, a.cleanupLoc)
				continue
			}

			a.logger.Warn("starting scheduled instance cleanup run",
				"scheduled_kst", next.In(a.cleanupLoc).Format(time.RFC3339),
				"started_kst", now.In(a.cleanupLoc).Format(time.RFC3339),
				"window_end_kst", windowEnd.In(a.cleanupLoc).Format(time.RFC3339),
			)
			a.reconcileDestroyAllInstances(ctx, windowEnd)
			next = nextCleanupTimeKST(time.Now(), a.cleanupLoc)
		}
	}
}

func nextCleanupTimeKST(now time.Time, loc *time.Location) time.Time {
	localNow := now.In(loc)
	scheduled := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		cleanupHourKST,
		cleanupMinuteKST,
		0,
		0,
		loc,
	)

	if !localNow.Before(scheduled) {
		scheduled = scheduled.Add(24 * time.Hour)
	}

	return scheduled
}

func firstCleanupRunTimeKST(now time.Time, loc *time.Location) time.Time {
	if !isWithinCleanupWindow(now, loc) {
		return nextCleanupTimeKST(now, loc)
	}

	localNow := now.In(loc)
	scheduledToday := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		cleanupHourKST,
		cleanupMinuteKST,
		0,
		0,
		loc,
	)
	if !localNow.Before(scheduledToday) {
		return now
	}

	return scheduledToday
}

func cleanupWindowBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	localNow := now.In(loc)
	windowStart := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		cleanupWindowStartHourKST,
		cleanupWindowStartMinuteKST,
		0,
		0,
		loc,
	)
	windowEnd := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		cleanupWindowEndHourKST,
		cleanupWindowEndMinuteKST,
		0,
		0,
		loc,
	)
	return windowStart, windowEnd
}

func isWithinCleanupWindow(now time.Time, loc *time.Location) bool {
	windowStart, windowEnd := cleanupWindowBounds(now, loc)
	localNow := now.In(loc)
	if localNow.Before(windowStart) {
		return false
	}
	return localNow.Before(windowEnd)
}

func (a *app) reconcileDestroyAllInstances(ctx context.Context, cutoff time.Time) {
	backoff := a.cleanupBackoffMin

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if !time.Now().Before(cutoff) {
			a.logger.Warn("cleanup reconciliation stopped at window cutoff",
				"cutoff_kst", cutoff.In(a.cleanupLoc).Format(time.RFC3339),
			)
			return
		}

		instances, err := a.vultr.listAllInstances(ctx)
		if err != nil {
			a.logger.Error("cleanup reconciliation failed to list instances", "error", err, "retry_in", backoff.String())
			if !sleepWithContextUntil(ctx, backoff, cutoff) {
				return
			}
			backoff = nextBackoff(backoff, a.cleanupBackoffMax)
			continue
		}

		if len(instances) == 0 {
			a.logger.Info("cleanup reconciliation complete", "remaining_instances", 0)
			return
		}

		a.logger.Warn("cleanup reconciliation deleting instances", "count", len(instances))

		deleteFailures := 0
		for _, instance := range instances {
			if !time.Now().Before(cutoff) {
				a.logger.Warn("cleanup reconciliation reached window cutoff during delete pass",
					"cutoff_kst", cutoff.In(a.cleanupLoc).Format(time.RFC3339),
				)
				return
			}

			if instance.ID == "" {
				deleteFailures++
				a.logger.Error("cleanup reconciliation found instance without id", "label", instance.Label, "ip", instance.MainIP)
				continue
			}

			err := a.vultr.deleteInstance(ctx, instance.ID)
			if err != nil {
				deleteFailures++
				a.logger.Error("cleanup reconciliation failed to delete instance",
					"instance_id", instance.ID,
					"label", instance.Label,
					"error", err,
				)
				continue
			}

			a.logger.Info("cleanup reconciliation delete requested", "instance_id", instance.ID, "label", instance.Label)

			// Keep a short gap between delete calls to reduce burst rate against the API.
			if !sleepWithContextUntil(ctx, a.cleanupPassDeleteInterval, cutoff) {
				return
			}
		}

		if deleteFailures > 0 {
			a.logger.Warn("cleanup reconciliation pass incomplete", "delete_failures", deleteFailures, "retry_in", backoff.String())
			if !sleepWithContextUntil(ctx, backoff, cutoff) {
				return
			}
			backoff = nextBackoff(backoff, a.cleanupBackoffMax)
			continue
		}

		// Deletions are asynchronous upstream; allow state to settle before verifying again.
		if !sleepWithContextUntil(ctx, a.cleanupSettleDelay, cutoff) {
			return
		}
		backoff = a.cleanupBackoffMin
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func sleepWithContextUntil(ctx context.Context, d time.Duration, cutoff time.Time) bool {
	if cutoff.IsZero() {
		return sleepWithContext(ctx, d)
	}

	remaining := time.Until(cutoff)
	if remaining <= 0 {
		return false
	}

	wait := d
	if wait > remaining {
		wait = remaining
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return time.Now().Before(cutoff)
	}
}

func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}
