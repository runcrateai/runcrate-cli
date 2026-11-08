package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StorageVolume is a volume as GET /api/v1/storage answers it (camelCase, the API's StorageVolume):
// Region is the id of the region it was made in, one of /api/v1/storage/regions (a volume made long
// ago may hold a region name instead).
type StorageVolume struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SizeGB    int    `json:"sizeGb"`
	Region    string `json:"region"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

type StorageRegion struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

type CreateStorageRequest struct {
	Name   string `json:"name"`
	SizeGB int    `json:"size_gb"`
	Region string `json:"region,omitempty"`
}

func (c *Client) ListStorage() ([]StorageVolume, error) {
	resp, err := c.do("GET", "/api/v1/storage", nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[[]StorageVolume]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}

func (c *Client) CreateStorage(req CreateStorageRequest) (*StorageVolume, error) {
	body, _ := json.Marshal(req)
	resp, err := c.do("POST", "/api/v1/storage", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[StorageVolume]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

func (c *Client) DeleteStorage(id string) error {
	resp, err := c.do("DELETE", "/api/v1/storage/"+id, nil)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return nil
	}
	return checkHTTPStatus(resp)
}

func (c *Client) ResizeStorage(id string, newSizeGB int) error {
	body := fmt.Sprintf(`{"size_gb":%d}`, newSizeGB)
	resp, err := c.do("PATCH", "/api/v1/storage/"+id, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	return checkHTTPStatus(resp)
}

func (c *Client) ListStorageRegions() ([]StorageRegion, error) {
	resp, err := c.do("GET", "/api/v1/storage/regions", nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[[]StorageRegion]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}
