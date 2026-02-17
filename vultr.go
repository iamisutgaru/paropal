package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c *vultrClient) pendingCharges(ctx context.Context) (float64, error) {
	var response accountResponse
	if err := c.do(ctx, http.MethodGet, "/account", &response); err != nil {
		return 0, err
	}

	return response.Account.PendingCharges, nil
}

func (c *vultrClient) firstInstanceWithLabelPrefix(ctx context.Context, prefix string) (*vultrInstance, error) {
	instances, err := c.listAllInstances(ctx)
	if err != nil {
		return nil, err
	}

	for _, instance := range instances {
		if strings.HasPrefix(instance.Label, prefix) {
			match := instance
			return &match, nil
		}
	}

	return nil, errInstanceNotFound
}

func (c *vultrClient) listAllInstances(ctx context.Context) ([]vultrInstance, error) {
	cursor := ""
	instances := make([]vultrInstance, 0, 16)

	for {
		params := url.Values{}
		params.Set("per_page", "100")
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		path := "/instances?" + params.Encode()
		var response listInstancesResponse
		if err := c.do(ctx, http.MethodGet, path, &response); err != nil {
			return nil, err
		}

		instances = append(instances, response.Instances...)

		nextCursor, err := extractCursor(response.Meta.Links.Next)
		if err != nil {
			return nil, err
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return instances, nil
}

func (c *vultrClient) deleteInstance(ctx context.Context, instanceID string) error {
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("instance id cannot be empty")
	}

	path := "/instances/" + url.PathEscape(instanceID)
	return c.do(ctx, http.MethodDelete, path, nil)
}

func (c *vultrClient) do(ctx context.Context, method, path string, dest any) error {
	endpoint := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("vultr %s returned %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}

	if dest == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("decode %s response: %w", path, err)
	}

	return nil
}

func extractCursor(nextLink string) (string, error) {
	nextLink = strings.TrimSpace(nextLink)
	if nextLink == "" {
		return "", nil
	}

	parsed, err := url.Parse(nextLink)
	if err != nil {
		return "", fmt.Errorf("parse pagination link %q: %w", nextLink, err)
	}

	return parsed.Query().Get("cursor"), nil
}
