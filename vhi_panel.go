package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"time"
)

// VHIPanelConfig holds config for the VHI admin panel API.
type VHIPanelConfig struct {
	BaseURL  string // e.g. https://10.21.0.240:8888
	Username string
	Password string
	Domain   string
	Insecure bool
}

// VHIPanelClient interacts with the VHI admin panel API (port 8888).
type VHIPanelClient struct {
	config     VHIPanelConfig
	httpClient *http.Client
	token      string
	cookies    []*http.Cookie // session cookies from login
}

// PanelStat represents the VHI panel /api/v2/compute/cluster/stat response.
// This is the same data source used by the VHI dashboard.
type PanelStat struct {
	Datetime string `json:"datetime"`
	Compute  struct {
		CPUAllocationRatio float64 `json:"cpu_allocation_ratio"`
		RAMAllocationRatio float64 `json:"ram_allocation_ratio"`
		BlockCapacity      int64   `json:"block_capacity"`  // bytes - Provisioned Storage Total
		BlockUsage         int64   `json:"block_usage"`     // bytes - Provisioned Storage Used
		VCPUs              int     `json:"vcpus"`           // VMs vCPUs (only running)
		CPUUsage           float64 `json:"cpu_usage"`       // CPU usage percent
		VmMemUsage         int64   `json:"vm_mem_usage"`    // bytes - actual VM memory usage
		VmMemReserved      int64   `json:"vm_mem_reserved"` // bytes - reserved RAM for VMs
		VmMemFree          int64   `json:"vm_mem_free"`     // bytes - free RAM
		VmMemCapacity      int64   `json:"vm_mem_capacity"` // bytes - total RAM capacity (active)
		VCPUsFree          int     `json:"vcpus_free"`      // free vCPUs
		Hypervisors        int     `json:"hypervisors"`     // active hypervisor count
	} `json:"compute"`
	Servers struct {
		Count            int `json:"count"`
		Error            int `json:"error"`
		InProgress       int `json:"in_progress"`
		Running          int `json:"running"`
		Stopped          int `json:"stopped"`
		ShelvedOffloaded int `json:"shelved_offloaded"`
		Active           int `json:"active"`
		Shutoff          int `json:"shutoff"`
	} `json:"servers"`
	Fenced struct {
		PhysicalCPUCores int     `json:"physical_cpu_cores"`
		VCPUs            int     `json:"vcpus"`
		PhysicalCPUUsage float64 `json:"physical_cpu_usage"`
		PhysicalMemTotal int64   `json:"physical_mem_total"` // bytes - fenced RAM
		ReservedMemory   int64   `json:"reserved_memory"`
		VmMemCapacity    float64 `json:"vm_mem_capacity"`
	} `json:"fenced"`
	Physical struct {
		CPUUsage      float64 `json:"cpu_usage"`
		CPUCores      int     `json:"cpu_cores"`
		VCPUsTotal    int     `json:"vcpus_total"`
		MemTotal      int64   `json:"mem_total"`      // bytes - total physical RAM
		BlockFree     int64   `json:"block_free"`     // bytes - physical block free
		BlockCapacity int64   `json:"block_capacity"` // bytes - physical block capacity
	} `json:"physical"`
	Reserved struct {
		VCPUs  int   `json:"vcpus"`  // System vCPUs
		CPUs   int   `json:"cpus"`   // System physical CPUs
		Memory int64 `json:"memory"` // bytes - System RAM
	} `json:"reserved"`
	Volumes struct {
		Available   int `json:"available"`
		BackingUp   int `json:"backing-up"`
		ErrorDelete int `json:"error_deleting"`
		InUse       int `json:"in-use"`
		ReservedVol int `json:"reserved"`
		Count       int `json:"count"`
	} `json:"volumes"`
}

// StorageClusterStat represents the /api/v2/storage/cluster/stat response.
// This is the logical storage layer (vcpus, allocatable, free) — matches vstorage CLI output.
type StorageClusterStat struct {
	Cluster struct {
		Name string `json:"name"`
	} `json:"cluster"`
	Space struct {
		AllocatableTotal int64 `json:"allocatable_total"` // bytes — "377TB"
		AllocatableUsed  int64 `json:"allocatable_used"`  // bytes — "287TB"
		AllocatableFree  int64 `json:"allocatable_free"`  // bytes — "314TB free"
		PhysicalTotal    int64 `json:"physical_total"`    // bytes — "386TB"
		PhysicalFree     int64 `json:"physical_free"`     // bytes
	} `json:"space"`
	License struct {
		Capacity int64  `json:"capacity"` // bytes
		Used     int64  `json:"used"`     // bytes
		Status   string `json:"status"`
	} `json:"license"`
}

// NewVHIPanelClient creates a new VHI panel API client.
func NewVHIPanelClient(config VHIPanelConfig) *VHIPanelClient {
	tr := &http.Transport{}
	if config.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Cookie jar to automatically handle session cookies from login
	jar, _ := cookiejar.New(nil)

	return &VHIPanelClient{
		config: config,
		httpClient: &http.Client{
			Transport: tr,
			Timeout:   30 * time.Second,
			Jar:       jar,
			// Don't follow redirects (login may return 302)
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Login authenticates with the VHI panel and obtains a session token.
func (c *VHIPanelClient) Login() error {
	loginURL := fmt.Sprintf("%s/api/v2/login", c.config.BaseURL)

	// VHI panel login uses username + password
	loginBody := map[string]string{
		"username": c.config.Username,
		"password": c.config.Password,
	}

	bodyJSON, err := json.Marshal(loginBody)
	if err != nil {
		return fmt.Errorf("failed to marshal login body: %w", err)
	}

	log.Printf("VHI Panel login to: %s", loginURL)

	req, err := http.NewRequest("POST", loginURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	log.Printf("VHI Panel login response status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response — extract scoped_token
	var loginResp struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Token       string `json:"token"`
		ScopedToken string `json:"scoped_token"`
		ProjectID   string `json:"project_id"`
		DomainID    string `json:"domain_id"`
	}

	if err := json.Unmarshal(body, &loginResp); err != nil {
		return fmt.Errorf("failed to parse login response: %w (body: %s)", err, string(body))
	}

	// Use scoped_token as the auth token for subsequent requests
	if loginResp.ScopedToken != "" {
		c.token = loginResp.ScopedToken
		c.cookies = resp.Cookies() // save all cookies from login
		log.Printf("VHI Panel login successful, scoped_token obtained, %d cookies saved", len(c.cookies))
		return nil
	}

	// Fallback to token field
	if loginResp.Token != "" && loginResp.Token != "unscoped" {
		c.token = loginResp.Token
		log.Printf("VHI Panel login successful, token obtained")
		return nil
	}

	return fmt.Errorf("login succeeded but no usable token in response: %s", string(body))
}

// doAuthGet performs a GET request with auth headers, auto re-login on 401.
func (c *VHIPanelClient) doAuthGet(endpoint string) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if c.token == "" {
			if err := c.Login(); err != nil {
				return nil, fmt.Errorf("panel login failed: %w", err)
			}
		}

		url := fmt.Sprintf("%s%s", c.config.BaseURL, endpoint)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("X-Auth-Token", c.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("X-Session-Id", "0")
		for _, cookie := range c.cookies {
			cp := *cookie
			if cp.Name == "session" {
				cp.Name = "session0"
			}
			req.AddCookie(&cp)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		log.Printf("VHI Panel %s response status: %d", endpoint, resp.StatusCode)

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			// Token expired — force re-login and retry once
			log.Printf("VHI Panel token expired, re-logging in...")
			c.token = ""
			c.cookies = nil
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("request returned status %d: %s", resp.StatusCode, string(body))
		}
		return body, nil
	}
	return nil, fmt.Errorf("request failed after re-login")
}

// GetStat retrieves the cluster statistics from the VHI panel.
// This returns the exact same data shown on the VHI dashboard.
func (c *VHIPanelClient) GetStat() (*PanelStat, error) {
	body, err := c.doAuthGet("/api/v2/compute/cluster/stat")
	if err != nil {
		return nil, err
	}

	var stat PanelStat
	if err := json.Unmarshal(body, &stat); err != nil {
		return nil, fmt.Errorf("failed to decode stat response: %w (body: %s)", err, string(body))
	}

	log.Printf("VHI Panel stat: vCPUs=%d, System=%d, Free=%d, Fenced=%d, Block=%.2f TiB",
		stat.Compute.VCPUs, stat.Reserved.VCPUs, stat.Compute.VCPUsFree,
		stat.Fenced.VCPUs, float64(stat.Compute.BlockCapacity)/1099511627776.0)

	return &stat, nil
}

// GetStorageStat retrieves logical storage cluster statistics from /api/v2/storage/cluster/stat.
// This returns allocatable/used/free storage matching the vstorage CLI output (e.g. 287TB of 377TB).
func (c *VHIPanelClient) GetStorageStat() (*StorageClusterStat, error) {
	body, err := c.doAuthGet("/api/v2/storage/cluster/stat")
	if err != nil {
		return nil, err
	}

	var stat StorageClusterStat
	if err := json.Unmarshal(body, &stat); err != nil {
		return nil, fmt.Errorf("failed to decode storage stat response: %w (body: %s)", err, string(body))
	}

	bytesToTiB := 1024.0 * 1024.0 * 1024.0 * 1024.0
	log.Printf("VHI Panel storage: AllocTotal=%.1f TiB, AllocUsed=%.1f TiB, AllocFree=%.1f TiB",
		float64(stat.Space.AllocatableTotal)/bytesToTiB,
		float64(stat.Space.AllocatableUsed)/bytesToTiB,
		float64(stat.Space.AllocatableFree)/bytesToTiB)

	return &stat, nil
}
