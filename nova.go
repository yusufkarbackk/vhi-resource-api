package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NovaConfig menyimpan konfigurasi untuk Nova Compute API client.
type NovaConfig struct {
	BaseURL  string // e.g. https://10.21.0.240:8774
	Token    string
	Insecure bool
}

// NovaClient adalah HTTP client untuk Nova Compute API.
type NovaClient struct {
	config     NovaConfig
	httpClient *http.Client
}

// NovaFlavor merepresentasikan flavor dari sebuah server.
type NovaFlavor struct {
	ID    string `json:"id"`
	VCPUs int    `json:"vcpus"`
	RAM   int    `json:"ram"`  // in MB
	Disk  int    `json:"disk"` // in GB
}

// NovaServer merepresentasikan satu server/VM dari Nova API.
type NovaServer struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Status   string     `json:"status"` // ACTIVE, SHUTOFF, SHELVED_OFFLOADED, etc.
	TenantID string     `json:"tenant_id"`
	Flavor   NovaFlavor `json:"flavor"`
}

// novaServersResponse adalah response wrapper dari Nova list servers.
type novaServersResponse struct {
	Servers []NovaServer `json:"servers"`
}

// HypervisorStats merepresentasikan statistik aggregate dari semua hypervisors.
type HypervisorStats struct {
	Count        int `json:"count"`
	VCPUs        int `json:"vcpus"`          // Total physical vCPUs * overcommit ratio
	VCPUsUsed    int `json:"vcpus_used"`     // vCPUs currently used
	MemoryMB     int `json:"memory_mb"`      // Total RAM in MB
	MemoryMBUsed int `json:"memory_mb_used"` // RAM currently used in MB
	FreeRAMMB    int `json:"free_ram_mb"`    // Free RAM in MB
	RunningVMs   int `json:"running_vms"`
	LocalGB      int `json:"local_gb"`
	LocalGBUsed  int `json:"local_gb_used"`
}

// Hypervisor merepresentasikan satu hypervisor node.
type Hypervisor struct {
	ID                 int    `json:"id"`
	Status             string `json:"status"` // enabled, disabled
	State              string `json:"state"`  // up, down
	VCPUs              int    `json:"vcpus"`
	MemoryMB           int    `json:"memory_mb"`
	LocalGB            int    `json:"local_gb"`
	VCPUsUsed          int    `json:"vcpus_used"`
	MemoryMBUsed       int    `json:"memory_mb_used"`
	LocalGBUsed        int    `json:"local_gb_used"`
	FreeRAMMB          int    `json:"free_ram_mb"`
	FreeDiskGB         int    `json:"free_disk_gb"`
	HypervisorHostname string `json:"hypervisor_hostname"`
}

// hypervisorsResponse adalah response dari GET /os-hypervisors/detail
type hypervisorsResponse struct {
	Hypervisors []Hypervisor `json:"hypervisors"`
}

type hypervisorStatsResponse struct {
	HypervisorStatistics HypervisorStats `json:"hypervisor_statistics"`
}

// NewNovaClient membuat Nova client baru.
func NewNovaClient(config NovaConfig) *NovaClient {
	tr := &http.Transport{}

	if config.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	httpClient := &http.Client{
		Transport: tr,
		Timeout:   60 * time.Second,
	}

	return &NovaClient{
		config:     config,
		httpClient: httpClient,
	}
}

// GetHypervisorStats mengambil statistik aggregate dari semua hypervisors.
// GET /v2.1/os-hypervisors/statistics
func (c *NovaClient) GetHypervisorStats() (*HypervisorStats, error) {
	url := fmt.Sprintf("%s/v2.1/os-hypervisors/statistics", c.config.BaseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create hypervisor stats request: %w", err)
	}

	req.Header.Set("X-Auth-Token", c.config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute hypervisor stats request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hypervisor stats returned status %d: %s", resp.StatusCode, string(body))
	}

	var result hypervisorStatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode hypervisor stats: %w", err)
	}

	return &result.HypervisorStatistics, nil
}

// GetHypervisors mengambil daftar detail semua hypervisors.
// GET /v2.1/os-hypervisors/detail
func (c *NovaClient) GetHypervisors() ([]Hypervisor, error) {
	url := fmt.Sprintf("%s/v2.1/os-hypervisors/detail", c.config.BaseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create hypervisors request: %w", err)
	}

	req.Header.Set("X-Auth-Token", c.config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute hypervisors request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hypervisors returned status %d: %s", resp.StatusCode, string(body))
	}

	var result hypervisorsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode hypervisors: %w", err)
	}

	return result.Hypervisors, nil
}

// ListAllServers mengambil semua servers di cluster menggunakan
// GET /v2.1/servers/detail?all_tenants=true
// dengan pagination otomatis menggunakan marker.
func (c *NovaClient) ListAllServers() ([]NovaServer, error) {
	var allServers []NovaServer

	baseURL := fmt.Sprintf("%s/v2.1/servers/detail?all_tenants=true&limit=200", c.config.BaseURL)
	nextURL := baseURL

	for nextURL != "" {
		req, err := http.NewRequest("GET", nextURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Nova request: %w", err)
		}

		req.Header.Set("X-Auth-Token", c.config.Token)
		req.Header.Set("Content-Type", "application/json")
		// Microversion 2.47+ embeds flavor details (vcpus, ram, disk) directly in server response
		req.Header.Set("OpenStack-API-Version", "compute 2.47")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to execute Nova request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("Nova API returned status %d: %s", resp.StatusCode, string(body))
		}

		var result novaServersResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("failed to decode Nova response: %w", err)
		}

		if len(result.Servers) == 0 {
			break
		}

		allServers = append(allServers, result.Servers...)

		// Pagination: gunakan marker dari server terakhir
		if len(result.Servers) >= 200 {
			lastID := result.Servers[len(result.Servers)-1].ID
			nextURL = fmt.Sprintf("%s&marker=%s", baseURL, lastID)
		} else {
			nextURL = ""
		}
	}

	return allServers, nil
}
