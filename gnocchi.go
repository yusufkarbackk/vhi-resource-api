package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
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
