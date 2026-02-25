package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// CinderConfig menyimpan konfigurasi untuk Cinder Block Storage API client.
type CinderConfig struct {
	BaseURL   string // e.g. https://10.21.0.240:8776
	Token     string
	ProjectID string // admin project ID, required for Cinder v3 API path
	Insecure  bool
}

// CinderClient adalah HTTP client untuk Cinder Block Storage API.
type CinderClient struct {
	config     CinderConfig
	httpClient *http.Client
}

// CinderVolume merepresentasikan satu Cinder volume dengan detail lengkap.
type CinderVolume struct {
	ID               string                   `json:"id"`
	Name             string                   `json:"name"`
	Size             int                      `json:"size"` // in GiB
	Status           string                   `json:"status"`
	Bootable         string                   `json:"bootable"` // "true" or "false"
	VolumeType       string                   `json:"volume_type"`
	Multiattach      bool                     `json:"multiattach"`
	Attachments      []map[string]interface{} `json:"attachments"`
	AvailabilityZone string                   `json:"availability_zone"`
}

type cinderVolumesResponse struct {
	Volumes []CinderVolume `json:"volumes"`
}

// StorageBreakdown berisi breakdown per kategori.
type StorageBreakdown struct {
	Count   int     `json:"count"`
	SizeGiB int     `json:"size_gib"`
	SizeTiB float64 `json:"size_tib"`
}

// StorageStats berisi aggregate provisioned storage statistics.
type StorageStats struct {
	TotalVolumes int
	AllSizeGiB   int

	// Breakdown by status
	ByStatus map[string]*StorageBreakdown

	// Breakdown by bootable
	ByBootable map[string]*StorageBreakdown

	// Breakdown by volume_type
	ByVolumeType map[string]*StorageBreakdown

	// Breakdown by availability_zone
	ByAZ map[string]*StorageBreakdown

	// Breakdown: attached vs unattached
	Attached   *StorageBreakdown
	Unattached *StorageBreakdown

	// Boot volumes attached to VMs
	BootAttached *StorageBreakdown
}

// NewCinderClient membuat Cinder client baru.
func NewCinderClient(config CinderConfig) *CinderClient {
	tr := &http.Transport{}

	if config.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	httpClient := &http.Client{
		Transport: tr,
		Timeout:   60 * time.Second,
	}

	return &CinderClient{
		config:     config,
		httpClient: httpClient,
	}
}

// ListAllVolumes mengambil semua Cinder volumes di cluster.
func (c *CinderClient) ListAllVolumes() ([]CinderVolume, error) {
	if c.config.ProjectID == "" {
		return nil, fmt.Errorf("project_id is required for Cinder API")
	}

	var allVolumes []CinderVolume

	baseURL := fmt.Sprintf("%s/v3/%s/volumes/detail?all_tenants=true&limit=500",
		c.config.BaseURL, c.config.ProjectID)
	nextURL := baseURL

	for nextURL != "" {
		req, err := http.NewRequest("GET", nextURL, nil)
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

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
		}

		var result cinderVolumesResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		if len(result.Volumes) == 0 {
			break
		}

		allVolumes = append(allVolumes, result.Volumes...)

		if len(result.Volumes) >= 500 {
			lastID := result.Volumes[len(result.Volumes)-1].ID
			nextURL = fmt.Sprintf("%s&marker=%s", baseURL, lastID)
		} else {
			nextURL = ""
		}
	}

	log.Printf("Fetched %d total Cinder volumes", len(allVolumes))
	return allVolumes, nil
}

func addToBreakdown(m map[string]*StorageBreakdown, key string, sizeGiB int) {
	if _, ok := m[key]; !ok {
		m[key] = &StorageBreakdown{}
	}
	m[key].Count++
	m[key].SizeGiB += sizeGiB
	m[key].SizeTiB = float64(m[key].SizeGiB) / 1024.0
}

// GetProvisionedStorage mengambil semua volumes dan menghitung storage stats.
func (c *CinderClient) GetProvisionedStorage() (*StorageStats, error) {
	volumes, err := c.ListAllVolumes()
	if err != nil {
		return nil, err
	}

	stats := &StorageStats{
		ByStatus:     make(map[string]*StorageBreakdown),
		ByBootable:   make(map[string]*StorageBreakdown),
		ByVolumeType: make(map[string]*StorageBreakdown),
		ByAZ:         make(map[string]*StorageBreakdown),
		Attached:     &StorageBreakdown{},
		Unattached:   &StorageBreakdown{},
		BootAttached: &StorageBreakdown{},
	}

	for _, vol := range volumes {
		stats.TotalVolumes++
		stats.AllSizeGiB += vol.Size

		// By status
		addToBreakdown(stats.ByStatus, vol.Status, vol.Size)

		// By bootable
		addToBreakdown(stats.ByBootable, vol.Bootable, vol.Size)

		// By volume type
		vt := vol.VolumeType
		if vt == "" {
			vt = "(empty)"
		}
		addToBreakdown(stats.ByVolumeType, vt, vol.Size)

		// By availability zone
		az := vol.AvailabilityZone
		if az == "" {
			az = "(empty)"
		}
		addToBreakdown(stats.ByAZ, az, vol.Size)

		// Attached vs unattached
		if len(vol.Attachments) > 0 {
			stats.Attached.Count++
			stats.Attached.SizeGiB += vol.Size
			stats.Attached.SizeTiB = float64(stats.Attached.SizeGiB) / 1024.0

			// Boot volumes that are attached
			if vol.Bootable == "true" {
				stats.BootAttached.Count++
				stats.BootAttached.SizeGiB += vol.Size
				stats.BootAttached.SizeTiB = float64(stats.BootAttached.SizeGiB) / 1024.0
			}
		} else {
			stats.Unattached.Count++
			stats.Unattached.SizeGiB += vol.Size
			stats.Unattached.SizeTiB = float64(stats.Unattached.SizeGiB) / 1024.0
		}
	}

	// Log semua breakdown
	log.Printf("===== CINDER VOLUME BREAKDOWN =====")
	log.Printf("Total: %d volumes, %d GiB (%.2f TiB)", stats.TotalVolumes, stats.AllSizeGiB, float64(stats.AllSizeGiB)/1024.0)

	log.Printf("\n--- By Status ---")
	for k, v := range stats.ByStatus {
		log.Printf("  %s: %d volumes, %.2f TiB", k, v.Count, v.SizeTiB)
	}

	log.Printf("\n--- By Bootable ---")
	for k, v := range stats.ByBootable {
		log.Printf("  bootable=%s: %d volumes, %.2f TiB", k, v.Count, v.SizeTiB)
	}

	log.Printf("\n--- By Volume Type ---")
	for k, v := range stats.ByVolumeType {
		log.Printf("  %s: %d volumes, %.2f TiB", k, v.Count, v.SizeTiB)
	}

	log.Printf("\n--- By Availability Zone ---")
	for k, v := range stats.ByAZ {
		log.Printf("  %s: %d volumes, %.2f TiB", k, v.Count, v.SizeTiB)
	}

	log.Printf("\n--- Attached vs Unattached ---")
	log.Printf("  Attached: %d volumes, %.2f TiB", stats.Attached.Count, stats.Attached.SizeTiB)
	log.Printf("  Unattached: %d volumes, %.2f TiB", stats.Unattached.Count, stats.Unattached.SizeTiB)
	log.Printf("  Boot+Attached: %d volumes, %.2f TiB", stats.BootAttached.Count, stats.BootAttached.SizeTiB)

	log.Printf("===================================")

	return stats, nil
}
