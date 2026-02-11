package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// Simple total usage response
type TotalUsage struct {
	Timestamp    string       `json:"timestamp"`
	TotalVMs     int          `json:"total_vms"`
	CPUCoresUsed float64      `json:"cpu_cores_used"` // Total vCPU cores terpakai
	RAMUsedGB    float64      `json:"ram_used_gb"`    // Total RAM terpakai (GiB)
	Errors       []UsageError `json:"errors,omitempty"`
}

// UsageError merepresentasikan kegagalan parsial saat mengambil usage dari VM/domain tertentu.
// Sesuai PRD, total tetap dikembalikan (parsial) bersama daftar error ini.
type UsageError struct {
	DomainName string `json:"domain_name,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	ProjectID  string `json:"project_id,omitempty"`
	Error      string `json:"error"`
}

// GET /api/v1/usage/total
// Mendapatkan total usage untuk SEMUA VM di semua domain/project
// FIXED VERSION - Removes early return that was causing 0 GB RAM

func getTotalUsage(w http.ResponseWriter, r *http.Request) {
	// Batas waktu global untuk operasi ini (sesuai PRD: maksimal 5 menit)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	// Baca daftar nama domain dari file (satu nama per baris)
	domainFile := getEnv("DOMAINS_FILE", "")
	domainNames, err := LoadDomainNames(domainFile)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load domain list from %s: %v", domainFile, err), http.StatusInternalServerError)
		return
	}
	if len(domainNames) == 0 {
		http.Error(w, "no domains configured in domain.txt", http.StatusBadRequest)
		return
	}

	// Login admin ke Keystone untuk mendapatkan admin token (X-Subject-Token)
	adminToken, err := GetAdminToken(ctx)
	if err != nil {
		log.Printf("Error: failed to get admin token: %v", err)
		http.Error(w, fmt.Sprintf("failed to authenticate admin: %v", err), http.StatusUnauthorized)
		return
	}

	// Bangun peta projectID -> domainName berdasarkan domainNames
	projectToDomain := make(map[string]string)

	var usageErrors []UsageError
	var errMu sync.Mutex

	for _, domainName := range domainNames {
		if ctx.Err() != nil {
			errMu.Lock()
			usageErrors = append(usageErrors, UsageError{
				DomainName: domainName,
				Error:      fmt.Sprintf("context cancelled while resolving domain: %v", ctx.Err()),
			})
			errMu.Unlock()
			break
		}

		projects, err := ListProjectsForDomainName(ctx, adminToken, domainName)
		if err != nil {
			log.Printf("Warning: failed to list projects for domain %s: %v", domainName, err)
			errMu.Lock()
			usageErrors = append(usageErrors, UsageError{
				DomainName: domainName,
				Error:      fmt.Sprintf("failed to list projects for domain: %v", err),
			})
			errMu.Unlock()
			continue
		}

		if len(projects) == 0 {
			errMu.Lock()
			usageErrors = append(usageErrors, UsageError{
				DomainName: domainName,
				Error:      "no projects found for domain",
			})
			errMu.Unlock()
			continue
		}

		for _, p := range projects {
			projectToDomain[p.ID] = domainName
		}
	}

	log.Printf("Project to Domain mapping: %d projects across %d domains", len(projectToDomain), len(domainNames))

	var totalCPUCoresUsed float64
	var totalRAMUsedGB float64
	var totalVMs int
	var mu sync.Mutex

	// Client Gnocchi dengan admin token (tidak lagi membaca GNOCCHI_TOKEN dari .env)
	baseURL := getEnv("GNOCCHI_URL", "")
	gnocchiClient := NewGnocchiClient(GnocchiConfig{
		BaseURL:  baseURL,
		Token:    adminToken,
		Insecure: true,
	})

	log.Println("Fetching all instances from Gnocchi with admin token...")
	instances, err := gnocchiClient.GetAllInstances()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get instances from Gnocchi: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Found %d total instances in Gnocchi", len(instances))

	// Filter instance berdasarkan mapping project -> domain
	type instanceWithDomain struct {
		Instance   GnocchiInstance
		DomainName string
	}

	var targets []instanceWithDomain
	for _, inst := range instances {
		if domainName, ok := projectToDomain[inst.ProjectID]; ok {
			targets = append(targets, instanceWithDomain{
				Instance:   inst,
				DomainName: domainName,
			})
		}
	}

	totalVMs = len(targets)
	log.Printf("Filtered to %d instances in target domains", totalVMs)

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Max 10 concurrent requests

	for _, t := range targets {
		t := t

		wg.Add(1)
		go func() {
			defer wg.Done()

			// Hargai batas paralel global
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Cek context sebelum kerja berat
			if ctx.Err() != nil {
				errMu.Lock()
				usageErrors = append(usageErrors, UsageError{
					DomainName: t.DomainName,
					InstanceID: t.Instance.ID,
					ProjectID:  t.Instance.ProjectID,
					Error:      fmt.Sprintf("context cancelled while processing instance: %v", ctx.Err()),
				})
				errMu.Unlock()
				return
			}

			inst := t.Instance

			// ===================================================================
			// Get vCPU count from "vcpus" metric
			// ===================================================================
			if vcpuMetricID, ok := inst.Metrics["vcpus"]; ok {
				measures, err := gnocchiClient.GetMetricMeasures(vcpuMetricID, "", "", 300)
				if err != nil {
					log.Printf("Warning: Failed to get vCPUs for instance %s (%s): %v", inst.DisplayName, inst.ID, err)
					errMu.Lock()
					usageErrors = append(usageErrors, UsageError{
						DomainName: t.DomainName,
						InstanceID: inst.ID,
						ProjectID:  inst.ProjectID,
						Error:      fmt.Sprintf("failed to get vcpus measures: %v", err),
					})
					errMu.Unlock()
				} else if len(measures) > 0 {
					vcpus := measures[len(measures)-1].Value
					log.Printf("Instance %s (%s): vCPUs = %.0f", inst.DisplayName, inst.ID, vcpus)
					mu.Lock()
					totalCPUCoresUsed += vcpus
					mu.Unlock()
				} else {
					log.Printf("Warning: Instance %s (%s) has vcpus metric but no data points", inst.DisplayName, inst.ID)
				}
			} else {
				log.Printf("Warning: Instance %s (%s) has no vcpus metric", inst.DisplayName, inst.ID)
			}

			// ===================================================================
			// Get RAM from "memory" metric (value in MB)
			// ===================================================================
			if memMetricID, ok := inst.Metrics["memory"]; ok {
				memMeasures, err := gnocchiClient.GetMetricMeasures(memMetricID, "", "", 300)
				if err != nil {
					log.Printf("Warning: Failed to get Memory for instance %s (%s): %v", inst.DisplayName, inst.ID, err)
					errMu.Lock()
					usageErrors = append(usageErrors, UsageError{
						DomainName: t.DomainName,
						InstanceID: inst.ID,
						ProjectID:  inst.ProjectID,
						Error:      fmt.Sprintf("failed to get memory measures: %v", err),
					})
					errMu.Unlock()
				} else if len(memMeasures) > 0 {
					memMB := memMeasures[len(memMeasures)-1].Value
					memGB := memMB / 1024.0
					log.Printf("Instance %s (%s): Memory = %.0f MB (%.2f GB)", inst.DisplayName, inst.ID, memMB, memGB)
					mu.Lock()
					totalRAMUsedGB += memGB
					mu.Unlock()
				} else {
					log.Printf("Warning: Instance %s (%s) has memory metric but no data points", inst.DisplayName, inst.ID)
				}
			} else {
				log.Printf("Warning: Instance %s (%s) has no memory metric. Available: %v",
					inst.DisplayName, inst.ID, getMetricKeys(inst.Metrics))
			}
		}()
	}

	wg.Wait()

	log.Printf("========================================")
	log.Printf("Total VMs in target domains: %d", totalVMs)
	log.Printf("Total CPU cores used: %.2f", totalCPUCoresUsed)
	log.Printf("Total RAM used: %.2f GB", totalRAMUsedGB)
	log.Printf("Errors encountered: %d", len(usageErrors))
	log.Printf("========================================")

	response := TotalUsage{
		Timestamp:    time.Now().Format(time.RFC3339),
		TotalVMs:     totalVMs,
		CPUCoresUsed: totalCPUCoresUsed,
		RAMUsedGB:    totalRAMUsedGB,
		Errors:       usageErrors,
	}

	w.Header().Set("Content-Type", "application/json")
	// Jika ada error parsial, gunakan 206 Partial Content
	if len(usageErrors) > 0 {
		w.WriteHeader(http.StatusPartialContent)
	}
	json.NewEncoder(w).Encode(response)
}

// Helper function to get metric keys for logging
func getMetricKeys(metrics map[string]string) []string {
	keys := make([]string, 0, len(metrics))
	for k := range metrics {
		keys = append(keys, k)
	}
	return keys
}
