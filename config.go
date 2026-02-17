package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

const (
	vultrBaseURL                     = "https://api.vultr.com/v2"
	labelPrefix                      = "paropal-"
	listenAddr                       = ":8080"
	requestTimeout                   = 10 * time.Second
	shutdownTimeout                  = 15 * time.Second
	shutdownTokenEnv                 = "SHUTDOWN_BEARER_TOKEN"
	cleanupTimeZone                  = "Asia/Seoul"
	cleanupHourKST                   = 0
	cleanupMinuteKST                 = 10
	cleanupWindowStartHourKST        = 0
	cleanupWindowStartMinuteKST      = 0
	cleanupWindowEndHourKST          = 7
	cleanupWindowEndMinuteKST        = 0
	createHourKST                    = 7
	createMinuteKST                  = 10
	labelTimeZone                    = "Asia/Tokyo"
	cloudInitTimeZone                = "Asia/Tokyo"
	cloudInitLocale                  = "en_US.UTF-8"
	provisionRegionID                = "nrt"
	provisionOSID                    = 2625
	provisionPlanID                  = "vhp-2c-2gb-amd"
	provisionUserScheme              = "limited"
	provisionSSHKeyID                = "c426659e-454e-40de-8a8b-6b9820fe72f2"
	provisionBlockStorageID          = "52cb7c3a-42fd-47e1-b120-6e8cf6b2ddd1"
	provisionBlockAttachLive         = false
	provisionPrimaryUser             = "linuxuser"
	defaultCleanupSettleDelay        = 20 * time.Second
	defaultCleanupBackoffMin         = 15 * time.Second
	defaultCleanupBackoffMax         = 5 * time.Minute
	defaultCleanupPassDeleteInterval = 2 * time.Second
	defaultProvisionBackoffMin       = 15 * time.Second
	defaultProvisionBackoffMax       = 5 * time.Minute
)

var errInstanceNotFound = errors.New("no instance found with matching label prefix")

type app struct {
	vultr                     *vultrClient
	logger                    *slog.Logger
	server                    *http.Server
	shutdownToken             string
	stopBackground            context.CancelFunc
	cleanupLoc                *time.Location
	labelLoc                  *time.Location
	cleanupSettleDelay        time.Duration
	cleanupBackoffMin         time.Duration
	cleanupBackoffMax         time.Duration
	cleanupPassDeleteInterval time.Duration
	provisionBackoffMin       time.Duration
	provisionBackoffMax       time.Duration
}

type vultrClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type accountResponse struct {
	Account struct {
		PendingCharges float64 `json:"pending_charges"`
	} `json:"account"`
}

type vultrInstance struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	MainIP string `json:"main_ip"`
	Label  string `json:"label"`
}

type listInstancesResponse struct {
	Instances []vultrInstance `json:"instances"`
	Meta      struct {
		Links struct {
			Next string `json:"next"`
		} `json:"links"`
	} `json:"meta"`
}
