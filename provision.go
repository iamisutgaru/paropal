package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

type provisionRunState struct {
	instanceID string
	label      string
}

func (a *app) runDailyProvision(ctx context.Context) {
	now := time.Now()
	next := firstProvisionRunTimeKST(now, a.cleanupLoc)
	a.logger.Info("daily instance provision scheduler started",
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
			a.logger.Info("daily instance provision scheduler stopped")
			return
		case <-timer.C:
			started := time.Now()
			a.logger.Warn("starting scheduled instance provision run",
				"scheduled_kst", next.In(a.cleanupLoc).Format(time.RFC3339),
				"started_kst", started.In(a.cleanupLoc).Format(time.RFC3339),
			)
			a.reconcileEnsureParopalInstance(ctx)
			next = nextProvisionTimeKST(time.Now(), a.cleanupLoc)
		}
	}
}

func nextProvisionTimeKST(now time.Time, loc *time.Location) time.Time {
	localNow := now.In(loc)
	scheduled := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		createHourKST,
		createMinuteKST,
		0,
		0,
		loc,
	)

	if !localNow.Before(scheduled) {
		scheduled = scheduled.Add(24 * time.Hour)
	}

	return scheduled
}

func firstProvisionRunTimeKST(now time.Time, loc *time.Location) time.Time {
	localNow := now.In(loc)
	scheduledToday := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		createHourKST,
		createMinuteKST,
		0,
		0,
		loc,
	)

	if localNow.Before(scheduledToday) {
		return scheduledToday
	}

	// Catch-up behavior: if the daemon starts after the scheduled time, run once immediately.
	return now
}

func (a *app) reconcileEnsureParopalInstance(ctx context.Context) {
	backoff := a.provisionBackoffMin
	var state provisionRunState

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		err := a.ensureParopalInstanceAndBlock(ctx, &state)
		if err == nil {
			return
		}

		a.logger.Error("instance provision failed", "error", err, "retry_in", backoff.String())
		if !sleepWithContext(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff, a.provisionBackoffMax)
	}
}

func (a *app) ensureParopalInstanceAndBlock(ctx context.Context, state *provisionRunState) error {
	// If we already created an instance in this run, don't create another one just because list endpoints are lagging.
	if state != nil && strings.TrimSpace(state.instanceID) != "" {
		attachErr := a.vultr.attachBlockStorage(ctx, provisionBlockStorageID, state.instanceID, provisionBlockAttachLive)
		if attachErr != nil {
			if isBlockAlreadyAttachedError(attachErr) {
				a.logger.Info("block storage already attached; continuing",
					"block_storage_id", provisionBlockStorageID,
					"instance_id", state.instanceID,
				)
				return nil
			}
			return fmt.Errorf("attach block storage: %w", attachErr)
		}

		a.logger.Info("block storage attach requested",
			"block_storage_id", provisionBlockStorageID,
			"instance_id", state.instanceID,
			"live", provisionBlockAttachLive,
		)
		return nil
	}

	instance, err := a.vultr.firstInstanceWithLabelPrefix(ctx, labelPrefix)
	if err != nil && !errors.Is(err, errInstanceNotFound) {
		return fmt.Errorf("list instances: %w", err)
	}

	if err == nil && instance != nil && isTerminatingInstanceStatus(instance.Status) {
		a.logger.Warn("ignoring terminating instance during provision",
			"instance_id", instance.ID,
			"label", instance.Label,
			"status", instance.Status,
			"ip", instance.MainIP,
		)
		err = errInstanceNotFound
	}

	createdNow := false
	if errors.Is(err, errInstanceNotFound) {
		cloudConfig, err := renderCloudConfig(provisionPrimaryUser)
		if err != nil {
			return err
		}
		userDataB64 := base64.StdEncoding.EncodeToString([]byte(cloudConfig))

		label := newInstanceLabel(time.Now(), a.labelLoc)
		instanceID, err := a.vultr.createInstance(ctx, createInstanceRequest{
			Region:     provisionRegionID,
			Plan:       provisionPlanID,
			OSID:       provisionOSID,
			Label:      label,
			SSHKeyID:   []string{provisionSSHKeyID},
			UserScheme: provisionUserScheme,
			UserData:   userDataB64,
		})
		if err != nil {
			return fmt.Errorf("create instance: %w", err)
		}

		createdNow = true
		if state != nil {
			state.instanceID = instanceID
			state.label = label
		}
		instance = &vultrInstance{
			ID:    instanceID,
			Label: label,
		}
		a.logger.Warn("created new instance",
			"instance_id", instanceID,
			"label", label,
		)
	} else {
		a.logger.Info("instance already exists; skipping create",
			"instance_id", instance.ID,
			"label", instance.Label,
			"status", instance.Status,
			"ip", instance.MainIP,
		)
	}

	attachErr := a.vultr.attachBlockStorage(ctx, provisionBlockStorageID, instance.ID, provisionBlockAttachLive)
	if attachErr != nil {
		if isBlockAlreadyAttachedError(attachErr) && !createdNow {
			a.logger.Info("block storage already attached; continuing",
				"block_storage_id", provisionBlockStorageID,
				"instance_id", instance.ID,
			)
			return nil
		}
		return fmt.Errorf("attach block storage: %w", attachErr)
	}

	a.logger.Info("block storage attach requested",
		"block_storage_id", provisionBlockStorageID,
		"instance_id", instance.ID,
		"live", provisionBlockAttachLive,
	)
	return nil
}

func newInstanceLabel(now time.Time, loc *time.Location) string {
	stamp := now.In(loc).Format("01-02_15-04-05")
	return labelPrefix + stamp
}

func isBlockAlreadyAttachedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already attached") || strings.Contains(msg, "already in use")
}

func isTerminatingInstanceStatus(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	if s == "" {
		return false
	}
	return strings.Contains(s, "destroy") ||
		strings.Contains(s, "delete") ||
		strings.Contains(s, "terminate") ||
		strings.Contains(s, "remov")
}
