package main

import (
	"bytes"
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

	var best *vultrInstance
	for i := range instances {
		instance := &instances[i]
		if !strings.HasPrefix(instance.Label, prefix) {
			continue
		}

		if best == nil {
			best = instance
			continue
		}

		// Prefer instances that have an IP assigned (more likely to be usable).
		if best.MainIP == "" && instance.MainIP != "" {
			best = instance
			continue
		}
		if best.MainIP != "" && instance.MainIP == "" {
			continue
		}

		// Prefer the lexicographically latest label (labels are timestamped).
		if instance.Label > best.Label {
			best = instance
		}
	}

	if best != nil {
		match := *best
		return &match, nil
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

func (c *vultrClient) reinstallInstance(ctx context.Context, instanceID string) error {
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("instance id cannot be empty")
	}

	path := "/instances/" + url.PathEscape(instanceID) + "/reinstall"
	return c.doJSON(ctx, http.MethodPost, path, struct{}{}, nil)
}

type createInstanceRequest struct {
	Region     string   `json:"region"`
	Plan       string   `json:"plan"`
	OSID       int      `json:"os_id"`
	Label      string   `json:"label"`
	SSHKeyID   []string `json:"sshkey_id,omitempty"`
	UserScheme string   `json:"user_scheme,omitempty"`
	UserData   string   `json:"user_data,omitempty"`
}

type createInstanceResponse struct {
	Instance struct {
		ID string `json:"id"`
	} `json:"instance"`
}

func (c *vultrClient) createInstance(ctx context.Context, req createInstanceRequest) (string, error) {
	var response createInstanceResponse
	if err := c.doJSON(ctx, http.MethodPost, "/instances", req, &response); err != nil {
		return "", err
	}

	instanceID := strings.TrimSpace(response.Instance.ID)
	if instanceID == "" {
		return "", errors.New("create instance response missing instance id")
	}

	return instanceID, nil
}

type attachBlockRequest struct {
	InstanceID string `json:"instance_id"`
	Live       bool   `json:"live"`
}

func (c *vultrClient) attachBlockStorage(ctx context.Context, blockStorageID, instanceID string, live bool) error {
	if strings.TrimSpace(blockStorageID) == "" {
		return errors.New("block storage id cannot be empty")
	}
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("instance id cannot be empty")
	}

	path := "/blocks/" + url.PathEscape(blockStorageID) + "/attach"
	return c.doJSON(ctx, http.MethodPost, path, attachBlockRequest{
		InstanceID: instanceID,
		Live:       live,
	}, nil)
}

func (c *vultrClient) do(ctx context.Context, method, path string, dest any) error {
	return c.doRequest(ctx, method, path, "", nil, dest)
}

func (c *vultrClient) doJSON(ctx context.Context, method, path string, request any, dest any) error {
	var body io.Reader
	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", path, err)
		}
		body = bytes.NewReader(data)
	}

	return c.doRequest(ctx, method, path, "application/json", body, dest)
}

func (c *vultrClient) doRequest(ctx context.Context, method, path, contentType string, body io.Reader, dest any) error {
	endpoint := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

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
