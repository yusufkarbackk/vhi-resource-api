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
	"net/url"
	"os"
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
	config         VHIPanelConfig
	httpClient     *http.Client
	token          string
	cookies        []*http.Cookie // session cookies from VHI panel login
	grafanaCookies []*http.Cookie // session cookies from Grafana login
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

// VStorageStat holds vstorage cluster metrics from Prometheus via Grafana proxy.
type VStorageStat struct {
	TotalBytes float64 // tier:mdsd_fs_space_bytes:sum
	FreeBytes  float64 // tier:mdsd_fs_free_space_bytes:sum
	UsedBytes  float64 // Total - Free
}

// promQueryResult is the minimal Prometheus /api/v1/query response structure.
type promQueryResult struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Value [2]json.RawMessage `json:"value"` // [timestamp, value_string]
		} `json:"result"`
	} `json:"data"`
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
		for _, ck := range c.cookies {
			log.Printf("  → Cookie: name=%q value=%.20s...", ck.Name, ck.Value)
		}
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

// loginGrafana obtains grafana_session by using the session0 cookie from the VHI panel login.
// The VHI panel SSO works as follows (confirmed from browser DevTools):
//  1. VHI panel login (/api/v2/login) returns a "session" cookie (UUID.Signature format).
//  2. Sending that cookie as "session0" to any Grafana endpoint triggers SSO and returns
//     a "grafana_session" cookie in the response.
//
// We do NOT need a separate Grafana form login — all the 405 endpoints we tried before
// were dead ends.
func (c *VHIPanelClient) loginGrafana() error {
	// Ensure we have a VHI panel session first.
	if c.token == "" {
		if err := c.Login(); err != nil {
			return fmt.Errorf("VHI panel login required before Grafana SSO: %w", err)
		}
	}

	// Build the session0 cookie list from the VHI panel login cookies.
	// The panel returns a cookie named "session"; Grafana expects it as "session0".
	var session0Cookies []*http.Cookie
	for _, ck := range c.cookies {
		cp := *ck
		if cp.Name == "session" {
			cp.Name = "session0"
		}
		session0Cookies = append(session0Cookies, &cp)
	}

	if len(session0Cookies) == 0 {
		return fmt.Errorf("no session cookies from VHI panel login — cannot do Grafana SSO")
	}

	// Hit /grafana/api/user with session0 — Grafana should validate via nginx SSO
	// and return a grafana_session cookie.
	grafanaUserURL := fmt.Sprintf("%s/grafana/api/user", c.config.BaseURL)
	req, err := http.NewRequest("GET", grafanaUserURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create Grafana SSO request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, ck := range session0Cookies {
		req.AddCookie(ck)
	}

	// Use a non-redirect client so we can capture Set-Cookie headers.
	noRedirectClient := &http.Client{
		Transport: c.httpClient.Transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return fmt.Errorf("Grafana SSO request failed: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	log.Printf("Grafana SSO /api/user → status %d", resp.StatusCode)

	// Collect all cookies from the response.
	c.grafanaCookies = session0Cookies // always carry session0
	for _, ck := range resp.Cookies() {
		log.Printf("  → Grafana SSO cookie: name=%q value=%.30s...", ck.Name, ck.Value)
		if ck.Name == "grafana_session" {
			c.grafanaCookies = append(c.grafanaCookies, ck)
			log.Printf("Grafana session obtained via SSO!")
		}
	}

	// If Grafana accepted the request (200 or redirect to dashboard), we're good.
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther {
		log.Printf("Grafana SSO succeeded (status %d)", resp.StatusCode)
		return nil
	}

	return fmt.Errorf("Grafana SSO failed: status %d body: %.200s", resp.StatusCode, string(body))
}

// doGrafanaGet performs a GET to a Grafana endpoint with grafana session cookies, auto re-login on 401.
func (c *VHIPanelClient) doGrafanaGet(fullURL string) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if len(c.grafanaCookies) == 0 {
			if err := c.loginGrafana(); err != nil {
				return nil, fmt.Errorf("grafana login failed: %w", err)
			}
		}

		req, err := http.NewRequest("GET", fullURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		// Send both grafana_session AND session0 cookies — Grafana SSO requires both
		for _, cookie := range c.grafanaCookies {
			req.AddCookie(cookie)
		}
		for _, cookie := range c.cookies {
			cp := *cookie
			if cp.Name == "session" {
				cp.Name = "session0"
			}
			req.AddCookie(&cp)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("grafana request failed: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		log.Printf("Grafana %s status: %d", fullURL, resp.StatusCode)

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			log.Printf("Grafana session expired, re-logging in...")
			c.grafanaCookies = nil
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("grafana request status %d: %.200s", resp.StatusCode, string(body))
		}
		return body, nil
	}
	return nil, fmt.Errorf("grafana request failed after re-login")
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

// queryPrometheusDirect queries a PromQL expression directly against a Prometheus server.
// This is the preferred method when PROMETHEUS_URL is set — no auth required.
func queryPrometheusDirect(prometheusURL, promql string) (float64, error) {
	fullURL := fmt.Sprintf("%s/api/v1/query?query=%s", prometheusURL, url.QueryEscape(promql))
	log.Printf("Prometheus direct query: %s", fullURL)

	// Plain HTTP client — Prometheus internal endpoint, no TLS needed.
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(fullURL)
	if err != nil {
		return 0, fmt.Errorf("prometheus direct GET failed: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	log.Printf("Prometheus direct status: %d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus direct returned status %d: %.200s", resp.StatusCode, string(body))
	}
	return parsePromResult(body, promql)
}

// queryPrometheusWithAPIKey queries a PromQL expression via the Grafana datasource proxy
// using a Grafana API key (Authorization: Bearer <key>). No SSO cookies needed.
// Create a key in: Grafana → Configuration → API Keys → Add API key (role: Viewer)
func (c *VHIPanelClient) queryPrometheusWithAPIKey(apiKey, promql string) (float64, error) {
	fullURL := fmt.Sprintf("%s/grafana/api/datasources/1/resources/api/v1/query?query=%s",
		c.config.BaseURL, url.QueryEscape(promql))

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("grafana API key request failed: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	log.Printf("Grafana API key query status: %d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("grafana API key returned status %d: %.200s", resp.StatusCode, string(body))
	}
	return parsePromResult(body, promql)
}

// queryPrometheus queries a PromQL expression via the Grafana datasource resources endpoint.
// Uses grafana_session + session0 cookies for auth (same as browser Grafana access).
// Fallback when PROMETHEUS_URL and GRAFANA_API_KEY are not set.
func (c *VHIPanelClient) queryPrometheus(promql string) (float64, error) {
	// Note: use /resources/ not /proxy/ — matches actual Grafana network requests
	fullURL := fmt.Sprintf("%s/grafana/api/datasources/1/resources/api/v1/query?query=%s",
		c.config.BaseURL, url.QueryEscape(promql))

	body, err := c.doGrafanaGet(fullURL)
	if err != nil {
		return 0, fmt.Errorf("prometheus query %q failed: %w", promql, err)
	}
	return parsePromResult(body, promql)
}

// parsePromResult parses a Prometheus /api/v1/query response and returns the scalar value.
func parsePromResult(body []byte, promql string) (float64, error) {
	var result promQueryResult
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("prometheus decode failed: %w (body: %.200s)", err, string(body))
	}
	if result.Status != "success" {
		return 0, fmt.Errorf("prometheus returned status %q (body: %.200s)", result.Status, string(body))
	}
	if len(result.Data.Result) == 0 {
		return 0, fmt.Errorf("prometheus returned no results for %q", promql)
	}

	// Value is [timestamp, "value_string"]
	var valStr string
	if err := json.Unmarshal(result.Data.Result[0].Value[1], &valStr); err != nil {
		return 0, fmt.Errorf("prometheus value decode failed: %w", err)
	}
	var val float64
	if _, err := fmt.Sscanf(valStr, "%f", &val); err != nil {
		return 0, fmt.Errorf("prometheus value parse failed: %w (raw: %s)", err, valStr)
	}
	return val, nil
}

// GetStorageStat retrieves vstorage logical storage metrics.
// Priority:
//  1. Direct Prometheus (PROMETHEUS_URL env) — no auth, simplest.
//  2. Grafana datasource proxy (requires SSO cookies) — fallback.
func (c *VHIPanelClient) GetStorageStat() (*VStorageStat, error) {
	const (
		queryTotal = `sum(tier:mdsd_fs_space_bytes:sum{cloud=""})`
		queryFree  = `sum(tier:mdsd_fs_free_space_bytes:sum{cloud=""})`
	)

	var queryFn func(string) (float64, error)

	switch {
	case os.Getenv("PROMETHEUS_URL") != "":
		// --- Option 1: Direct Prometheus (preferred, no auth needed) ---
		promURL := os.Getenv("PROMETHEUS_URL")
		log.Printf("vStorage source: direct Prometheus at %s", promURL)
		queryFn = func(q string) (float64, error) {
			return queryPrometheusDirect(promURL, q)
		}

	case os.Getenv("GRAFANA_API_KEY") != "":
		// --- Option 2: Grafana API key (no SSO needed) ---
		apiKey := os.Getenv("GRAFANA_API_KEY")
		log.Printf("vStorage source: Grafana API key")
		queryFn = func(q string) (float64, error) {
			return c.queryPrometheusWithAPIKey(apiKey, q)
		}

	default:
		// --- Option 3: Grafana SSO cookies (fallback, likely to fail) ---
		log.Printf("vStorage source: Grafana SSO proxy (set PROMETHEUS_URL or GRAFANA_API_KEY for better results)")
		if c.token == "" {
			if err := c.Login(); err != nil {
				return nil, fmt.Errorf("login failed: %w", err)
			}
		}
		queryFn = c.queryPrometheus
	}

	totalBytes, err := queryFn(queryTotal)
	if err != nil {
		return nil, fmt.Errorf("failed to get vstorage total: %w", err)
	}

	freeBytes, err := queryFn(queryFree)
	if err != nil {
		return nil, fmt.Errorf("failed to get vstorage free: %w", err)
	}

	stat := &VStorageStat{
		TotalBytes: totalBytes,
		FreeBytes:  freeBytes,
		UsedBytes:  totalBytes - freeBytes,
	}

	bytesToTiB := 1024.0 * 1024.0 * 1024.0 * 1024.0
	log.Printf("vStorage: Total=%.1f TiB, Used=%.1f TiB, Free=%.1f TiB",
		totalBytes/bytesToTiB, stat.UsedBytes/bytesToTiB, freeBytes/bytesToTiB)

	return stat, nil
}
