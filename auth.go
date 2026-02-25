package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"time"
)

// adminProjectID stores the admin project ID extracted from the Keystone token response.
// Used by Cinder API which requires project_id in the URL path.
var adminProjectID string

// DomainConfig merepresentasikan satu baris konfigurasi domain/project untuk login Keystone.
// Format file (per baris):
//
//	domain_name;project_id;username;password
type DomainConfig struct {
	DomainName string
	ProjectID  string
	Username   string
	Password   string
}

// LoadDomains membaca file konfigurasi domain (domains.txt) dan mengembalikan slice DomainConfig.
// Baris kosong atau yang diawali '#' akan di-skip.
func LoadDomains(path string) ([]DomainConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var domains []DomainConfig

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ";")
		if len(parts) < 4 {
			log.Printf("Warning: invalid domain line (need 4 fields): %q", line)
			continue
		}

		domains = append(domains, DomainConfig{
			DomainName: strings.TrimSpace(parts[0]),
			ProjectID:  strings.TrimSpace(parts[1]),
			Username:   strings.TrimSpace(parts[2]),
			Password:   strings.TrimSpace(parts[3]),
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return domains, nil
}

// KeystoneConfig menyimpan konfigurasi dasar untuk Keystone.
type KeystoneConfig struct {
	BaseURL  string
	Insecure bool
}

type KeystoneClient struct {
	config     KeystoneConfig
	httpClient *http.Client
}

func NewKeystoneClient(config KeystoneConfig) *KeystoneClient {
	tr := &http.Transport{}

	if config.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	httpClient := &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
	}

	return &KeystoneClient{
		config:     config,
		httpClient: httpClient,
	}
}

// GetToken melakukan login ke Keystone menggunakan kredensial DomainConfig
// dan mengembalikan X-Subject-Token yang kemudian dipakai sebagai X-Auth-Token ke Gnocchi.
func (c *KeystoneClient) GetToken(ctx context.Context, domain DomainConfig) (string, error) {
	if c == nil {
		return "", fmt.Errorf("keystone client is nil")
	}

	authPayload := map[string]interface{}{
		"auth": map[string]interface{}{
			"identity": map[string]interface{}{
				"methods": []string{"password"},
				"password": map[string]interface{}{
					"user": map[string]interface{}{
						"name":     domain.Username,
						"password": domain.Password,
						"domain": map[string]interface{}{
							"name": domain.DomainName,
						},
					},
				},
			},
			"scope": map[string]interface{}{
				"project": map[string]interface{}{
					"id": domain.ProjectID,
				},
			},
		},
	}

	body, err := json.Marshal(authPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal keystone auth payload: %w", err)
	}

	url := strings.TrimRight(c.config.BaseURL, "/") + "/auth/tokens"

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create keystone request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute keystone request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keystone returned non-2xx status: %d", resp.StatusCode)
	}

	token := resp.Header.Get("X-Subject-Token")
	if token == "" {
		return "", fmt.Errorf("keystone response missing X-Subject-Token header")
	}

	return token, nil
}

// AdminCredentials menyimpan kredensial admin OpenStack/Keystone yang digunakan
// untuk mendapatkan token admin (X-Subject-Token) sesuai PRD autentikasi.
type AdminCredentials struct {
	Username         string
	Password         string
	AdminDomainID    string
	AdminProjectName string
	AdminDomainName  string
}

// GetAdminToken membaca kredensial admin dari environment dan melakukan
// request ke Keystone untuk mendapatkan X-Subject-Token.
// Env yang digunakan:
//   - KEYSTONE_URL                (mis: https://10.21.0.240:5000/v3)
//   - ADMIN_USERNAME
//   - ADMIN_PASSWORD
//   - ADMIN_DOMAIN_ID             (domain.id untuk user admin)
//   - ADMIN_PROJECT_NAME          (nama project scope admin)
//   - ADMIN_PROJECT_DOMAIN_ID     (domain.id untuk project admin)
func GetAdminToken(ctx context.Context) (string, error) {
	baseURL := getEnv("KEYSTONE_URL", "")
	if baseURL == "" {
		return "", fmt.Errorf("KEYSTONE_URL is not set")
	}

	creds := AdminCredentials{
		Username:         getEnv("ADMIN_USERNAME", ""),
		Password:         getEnv("ADMIN_PASSWORD", ""),
		AdminDomainID:    getEnv("ADMIN_DOMAIN_ID", ""),
		AdminProjectName: getEnv("ADMIN_PROJECT_NAME", ""),
		AdminDomainName:  getEnv("ADMIN_DOMAIN_NAME", ""),
	}

	if creds.Username == "" || creds.Password == "" || creds.AdminDomainID == "" ||
		creds.AdminProjectName == "" || creds.AdminDomainName == "" {
		return "", fmt.Errorf("admin credentials are incomplete; please set ADMIN_USERNAME, ADMIN_PASSWORD, ADMIN_DOMAIN_ID, ADMIN_PROJECT_NAME, ADMIN_PROJECT_DOMAIN_ID")
	}

	client := NewKeystoneClient(KeystoneConfig{
		BaseURL:  baseURL,
		Insecure: true,
	})
	//fmt.Println(creds)
	return client.getAdminToken(ctx, creds)
}

// getAdminToken adalah implementasi internal yang membangun payload sesuai PRD:
//
//	{
//	  "auth": {
//	    "identity": {
//	      "methods": ["password"],
//	      "password": {
//	        "user": {
//	          "name": {username},
//	          "domain": { "id": {domain_id} },
//	          "password": {password}
//	        }
//	      }
//	    },
//	    "scope": {
//	      "project": {
//	        "name": {admin project name},
//	        "domain": { "id": {admin project domain_id} }
//	      }
//	    }
//	  }
//	}
func (c *KeystoneClient) getAdminToken(ctx context.Context, creds AdminCredentials) (string, error) {
	if c == nil {
		return "", fmt.Errorf("keystone client is nil")
	}

	authPayload := map[string]interface{}{
		"auth": map[string]interface{}{
			"identity": map[string]interface{}{
				"methods": []string{"password"},
				"password": map[string]interface{}{
					"user": map[string]interface{}{
						"name": creds.Username,
						"domain": map[string]interface{}{
							"name": creds.AdminDomainName,
						},
						"password": creds.Password,
					},
				},
			},
			"scope": map[string]interface{}{
				"project": map[string]interface{}{
					"name": creds.AdminProjectName,
					"domain": map[string]interface{}{
						"id": creds.AdminDomainID,
					},
				},
			},
		},
	}

	body, err := json.Marshal(authPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal keystone admin auth payload: %w", err)
	}

	urlStr := strings.TrimRight(c.config.BaseURL, "/") + ":5000/v3/auth/tokens"

	req, err := http.NewRequestWithContext(ctx, "POST", urlStr, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create keystone admin request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute keystone admin request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("keystone admin auth returned non-2xx status: %d", resp.StatusCode)
	}

	token := resp.Header.Get("X-Subject-Token")
	if token == "" {
		return "", fmt.Errorf("keystone admin response missing X-Subject-Token header")
	}

	// Parse response body to extract project_id
	var tokenResp struct {
		Token struct {
			Project struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"project"`
		} `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		log.Printf("Warning: could not parse token response body for project_id: %v", err)
	} else {
		adminProjectID = tokenResp.Token.Project.ID
		log.Printf("Admin project ID: %s (name: %s)", adminProjectID, tokenResp.Token.Project.Name)
	}

	return token, nil
}

// LoadDomainNames membaca file domain.txt yang berisi daftar nama domain (satu per baris).
// Baris kosong atau yang diawali '#' akan di-skip.
func LoadDomainNames(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var domains []string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domains = append(domains, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return domains, nil
}

// Struktur helper untuk response Keystone
type KeystoneDomain struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type keystoneDomainsResponse struct {
	Domains []KeystoneDomain `json:"domains"`
}

type KeystoneProject struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DomainID string `json:"domain_id"`
}

type keystoneProjectsResponse struct {
	Projects []KeystoneProject `json:"projects"`
}

// ListProjectsForDomainName mengembalikan daftar project untuk sebuah domain name
// dengan memanggil:
//   - GET /domains?name={domainName}
//   - GET /projects?domain_id={domainID}
func ListProjectsForDomainName(ctx context.Context, token, domainName string) ([]KeystoneProject, error) {
	baseURL := getEnv("KEYSTONE_URL", "")
	if baseURL == "" {
		return nil, fmt.Errorf("KEYSTONE_URL is not set")
	}

	client := NewKeystoneClient(KeystoneConfig{
		BaseURL:  baseURL,
		Insecure: true,
	})

	base := strings.TrimRight(client.config.BaseURL, "/")
	//fmt.Println(base)
	// 1) Resolve domain name -> domain id
	domainURL := fmt.Sprintf("%s:5000/v3/domains?name=%s", base, url.QueryEscape(domainName))
	//fmt.Println(domainURL)
	req, err := http.NewRequestWithContext(ctx, "GET", domainURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create domains request: %w", err)
	}
	req.Header.Set("X-Auth-Token", token)

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute domains request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("domains request returned status %d", resp.StatusCode)
	}

	var domResp keystoneDomainsResponse
	if err := json.NewDecoder(resp.Body).Decode(&domResp); err != nil {
		return nil, fmt.Errorf("failed to decode domains response: %w", err)
	}

	if len(domResp.Domains) == 0 {
		return nil, fmt.Errorf("no domain found with name %q", domainName)
	}

	domainID := domResp.Domains[0].ID

	// 2) List projects by domain_id
	projectsURL := fmt.Sprintf("%s:5000/v3/projects?domain_id=%s", base, url.QueryEscape(domainID))

	reqProj, err := http.NewRequestWithContext(ctx, "GET", projectsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create projects request: %w", err)
	}
	reqProj.Header.Set("X-Auth-Token", token)

	respProj, err := client.httpClient.Do(reqProj)
	if err != nil {
		return nil, fmt.Errorf("failed to execute projects request: %w", err)
	}
	defer respProj.Body.Close()

	if respProj.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("projects request returned status %d", respProj.StatusCode)
	}

	var projResp keystoneProjectsResponse
	if err := json.NewDecoder(respProj.Body).Decode(&projResp); err != nil {
		return nil, fmt.Errorf("failed to decode projects response: %w", err)
	}

	return projResp.Projects, nil
}
