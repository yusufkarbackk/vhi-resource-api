package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type GnocchiConfig struct {
	BaseURL  string
	Token    string
	Insecure bool
}

type GnocchiClient struct {
	config     GnocchiConfig
	httpClient *http.Client
}

type InstanceResource struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	DisplayName string            `json:"display_name"`
	FlavorName  string            `json:"flavor_name"`
	FlavorID    string            `json:"flavor_id"`
	Host        string            `json:"host"`
	CreatedAt   string            `json:"created_at"`
	StartedAt   string            `json:"started_at"`
	Metrics     map[string]string `json:"metrics"`
	ProjectID   string            `json:"project_id"`
	UserID      string            `json:"user_id"`
}

type MetricMeasure struct {
	Timestamp   string  `json:"timestamp"`
	Granularity float64 `json:"granularity"`
	Value       float64 `json:"value"`
}

func NewGnocchiClient(config GnocchiConfig) *GnocchiClient {
	tr := &http.Transport{}

	if config.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	httpClient := &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
	}

	return &GnocchiClient{
		config:     config,
		httpClient: httpClient,
	}
}

func (c *GnocchiClient) GetInstanceResource(instanceID string) (*InstanceResource, error) {
	url := fmt.Sprintf("%s/resource/instance/%s", c.config.BaseURL, instanceID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Auth-Token", c.config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var instance InstanceResource
	if err := json.NewDecoder(resp.Body).Decode(&instance); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &instance, nil
}

func (c *GnocchiClient) GetMetricMeasures(metricID, startDate, endDate string, granularity int) ([]MetricMeasure, error) {
	url := fmt.Sprintf("%s/metric/%s/measures?granularity=%d&aggregation=mean",
		c.config.BaseURL, metricID, granularity)

	if startDate != "" {
		url += fmt.Sprintf("&start=%s", startDate)
	}
	if endDate != "" {
		url += fmt.Sprintf("&stop=%s", endDate)
	}
	// fmt.Println(startDate)
	// fmt.Println(endDate)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Auth-Token", c.config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Gnocchi returns array of [timestamp, granularity, value]
	var rawMeasures [][]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rawMeasures); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to structured format
	measures := make([]MetricMeasure, 0, len(rawMeasures))
	for _, raw := range rawMeasures {
		if len(raw) != 3 {
			continue
		}

		timestamp, ok := raw[0].(string)
		if !ok {
			continue
		}

		granularity, ok := raw[1].(float64)
		if !ok {
			continue
		}

		value, ok := raw[2].(float64)
		if !ok {
			continue
		}

		measures = append(measures, MetricMeasure{
			Timestamp:   timestamp,
			Granularity: granularity,
			Value:       value,
		})
	}

	return measures, nil
}

// GetAllInstances retrieves all instance resources from Gnocchi
func (c *GnocchiClient) GetAllInstances() ([]GnocchiInstance, error) {
	url := fmt.Sprintf("%s/resource/instance", c.config.BaseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Auth-Token", c.config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	//fmt.Println(resp.Body)

	var instances []GnocchiInstance
	if err := json.NewDecoder(resp.Body).Decode(&instances); err != nil {
		return nil, err
	}

	return instances, nil
}

// GnocchiInstance is the simplified structure for instance list
type GnocchiInstance struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"display_name"`
	Metrics     map[string]string `json:"metrics"`
	ProjectID   string            `json:"project_id"`
}

// GnocchiProvisionedStorage berisi hasil aggregate provisioned storage dari Gnocchi.
type GnocchiProvisionedStorage struct {
	TotalGiB float64 // Sum of volume.size across all volumes (in GiB)
	TotalTiB float64 // Converted to TiB
}

// gnocchiAggregateResponse represents the response from POST /v1/aggregates
type gnocchiAggregateResponse struct {
	Measures struct {
		Aggregated [][]interface{} `json:"aggregated"` // [[timestamp, granularity, value], ...]
	} `json:"measures"`
}

// GetProvisionedStorage mengambil total provisioned storage dari Gnocchi
// menggunakan endpoint POST /v1/aggregates dengan metric volume.size.
// Ini adalah cara yang sama yang digunakan dashboard VHI.
func (c *GnocchiClient) GetProvisionedStorage() (*GnocchiProvisionedStorage, error) {
	// Use current time range - get the latest data point
	now := time.Now().UTC()
	// Look back 1 hour to get the most recent measurement
	start := now.Add(-1 * time.Hour).Format("2006-01-02T15:04:05")
	stop := now.Format("2006-01-02T15:04:05")

	// Gnocchi BaseURL from env already includes /v1 (e.g. https://10.21.0.240:8041/v1)
	// Do not add /v1 again
	url := fmt.Sprintf("%s/aggregates?details=False&needed_overlap=0.0&start=%s&stop=%s",
		c.config.BaseURL, start, stop)

	// Request body per VHI documentation
	// search: empty object {} = match all volumes across all projects (cluster-wide)
	body := map[string]interface{}{
		"operations":    "(aggregate sum (metric volume.size mean))",
		"search":        map[string]interface{}{},
		"resource_type": "volume",
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	log.Printf("Gnocchi aggregates URL: %s", url)
	log.Printf("Gnocchi aggregates body: %s", string(bodyJSON))

	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Auth-Token", c.config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	log.Printf("Gnocchi aggregates response status: %d", resp.StatusCode)
	log.Printf("Gnocchi aggregates response: %s", string(respBody))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response - try the documented format first
	var result gnocchiAggregateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Try parsing as a raw array (some Gnocchi versions return differently)
		var rawArray [][]interface{}
		if err2 := json.Unmarshal(respBody, &rawArray); err2 != nil {
			return nil, fmt.Errorf("failed to decode response: %w (raw: %s)", err, string(respBody))
		}
		// Use the last (most recent) data point
		if len(rawArray) > 0 {
			last := rawArray[len(rawArray)-1]
			if len(last) >= 3 {
				if val, ok := last[2].(float64); ok {
					log.Printf("Gnocchi provisioned storage (raw array): %.2f GiB = %.4f TiB", val, val/1024.0)
					return &GnocchiProvisionedStorage{
						TotalGiB: val,
						TotalTiB: val / 1024.0,
					}, nil
				}
			}
		}
		return nil, fmt.Errorf("no data points in response")
	}

	// Use the last (most recent) data point from measures.aggregated
	aggregated := result.Measures.Aggregated
	if len(aggregated) == 0 {
		return nil, fmt.Errorf("no aggregated data points returned")
	}

	last := aggregated[len(aggregated)-1]
	if len(last) < 3 {
		return nil, fmt.Errorf("invalid data point format")
	}

	value, ok := last[2].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid value type in data point")
	}

	log.Printf("Gnocchi provisioned storage: %.2f GiB = %.4f TiB", value, value/1024.0)

	return &GnocchiProvisionedStorage{
		TotalGiB: value,
		TotalTiB: value / 1024.0,
	}, nil
}
