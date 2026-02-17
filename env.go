package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

func newVultrClientFromEnv() (*vultrClient, error) {
	apiKey := strings.TrimSpace(os.Getenv("VULTR_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("VULTR_API_KEY environment variable is required")
	}

	return &vultrClient{
		apiKey:  apiKey,
		baseURL: vultrBaseURL,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}, nil
}

func shutdownTokenFromEnv() (string, error) {
	token := strings.TrimSpace(os.Getenv(shutdownTokenEnv))
	if token == "" {
		return "", fmt.Errorf("%s environment variable is required", shutdownTokenEnv)
	}

	return token, nil
}
