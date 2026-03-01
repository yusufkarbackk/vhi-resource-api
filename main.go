package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
)

// panelClient is a singleton initialized once at startup.
// Re-using the client across requests avoids re-login on every call.
var panelClient *VHIPanelClient

func main() {
	// Load .env file at startup so all getEnv() calls can read values
	if err := godotenv.Load("./.env"); err != nil {
		log.Printf("Warning: could not load .env file: %v", err)
	}

	// Initialize VHI panel client singleton (login once at startup)
	if url := getEnv("VHI_PANEL_URL", ""); url != "" {
		panelClient = NewVHIPanelClient(VHIPanelConfig{
			BaseURL:  url,
			Username: getEnv("ADMIN_USERNAME", "admin"),
			Password: getEnv("ADMIN_PASSWORD", ""),
			Domain:   getEnv("ADMIN_DOMAIN_NAME", "Default"),
			Insecure: true,
		})
		if err := panelClient.Login(); err != nil {
			log.Printf("Warning: VHI Panel initial login failed: %v", err)
		}
	}

	// Initialize Redis cache (optional — caching disabled if REDIS_HOST is not set)
	redisClient = initRedis()

	r := mux.NewRouter()

	// Health check — no auth required
	r.HandleFunc("/health", healthCheck).Methods("GET")

	// All /api/v1 routes require Bearer token auth
	api := r.PathPrefix("/api/v1").Subrouter()
	api.Use(bearerAuth)

	// Total usage snapshot endpoint (per-domain filtered, uses domain.txt)
	api.HandleFunc("/usage/total", getTotalUsage).Methods("GET")

	// Cluster-wide usage endpoint (all VMs in cluster, uses Nova API)
	api.HandleFunc("/usage/cluster", getClusterUsage).Methods("GET")

	// Billing endpoints
	api.HandleFunc("/billing/cpu/{instance_id}", getCPUBilling).Methods("GET")
	api.HandleFunc("/billing/resources/{instance_id}", getResourceBilling).Methods("GET")
	api.HandleFunc("/billing/report/{instance_id}", getBillingReport).Methods("GET")

	// Server configuration
	port := getEnv("PORT", "8080")
	log.Printf("Starting billing API server on port :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

// bearerAuth is a middleware that validates the Authorization: Bearer <token> header
// against the API_BEARER_TOKEN environment variable.
func bearerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := getEnv("API_BEARER_TOKEN", "")
		if expected == "" {
			log.Printf("ERROR: API_BEARER_TOKEN is not configured")
			http.Error(w, `{"error":"server misconfiguration"}`, http.StatusInternalServerError)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" || len(auth) < 8 || auth[:7] != "Bearer " {
			w.Header().Set("WWW-Authenticate", `Bearer realm="VHI Billing API"`)
			http.Error(w, `{"error":"missing or invalid Authorization header"}`, http.StatusUnauthorized)
			return
		}

		token := auth[7:]
		if token != expected {
			w.Header().Set("WWW-Authenticate", `Bearer realm="VHI Billing API"`)
			http.Error(w, `{"error":"invalid bearer token"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{
		"status": "healthy",
		"time":   time.Now().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getCPUBilling(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	instanceID := vars["instance_id"]
	fmt.Println("Fetching CPU billing for instance ID:", instanceID)
	// Get query parameters
	startDate := r.URL.Query().Get("start_date")
	endDate := r.URL.Query().Get("end_date")

	// Default to last month if not provided
	if startDate == "" || endDate == "" {
		now := time.Now()
		firstDay := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
		lastDay := time.Date(now.Year(), now.Month(), 0, 23, 59, 59, 0, time.UTC)
		startDate = firstDay.Format("2006-01-02T15:04:05")
		endDate = lastDay.Format("2006-01-02T15:04:05")
	}

	config := GnocchiConfig{
		BaseURL:  getEnv("GNOCCHI_URL", ""),
		Token:    getEnv("GNOCCHI_TOKEN", ""),
		Insecure: true,
	}

	fmt.Println(config.BaseURL)

	client := NewGnocchiClient(config)

	// Get instance resource
	instance, err := client.GetInstanceResource(instanceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get instance: %v", err), http.StatusInternalServerError)
		return
	}

	// Get CPU metric ID
	cpuMetricID, ok := instance.Metrics["cpu"]
	if !ok {
		http.Error(w, "CPU metric not found for instance", http.StatusNotFound)
		return
	}
	fmt.Println("Found CPU metric ID:", cpuMetricID)
	// Get CPU measures
	measures, err := client.GetMetricMeasures(cpuMetricID, startDate, endDate, 300) // 1 hour granularity
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get CPU measures: %v", err), http.StatusInternalServerError)
		return
	}

	// Calculate CPU usage
	numVCPUs := 2 // Default, should get from flavor
	if vcpuMetricID, ok := instance.Metrics["vcpus"]; ok {
		vcpuMeasures, _ := client.GetMetricMeasures(vcpuMetricID, startDate, endDate, 3600)
		if len(vcpuMeasures) > 0 {
			numVCPUs = int(vcpuMeasures[0].Value)
		}
	}

	usage := CalculateCPUUsage(measures, numVCPUs)
	billing := CalculateCPUBilling(usage, startDate, endDate)

	response := CPUBillingResponse{
		InstanceID:   instanceID,
		InstanceName: instance.DisplayName,
		StartDate:    startDate,
		EndDate:      endDate,
		VCPUs:        numVCPUs,
		Usage:        usage,
		Billing:      billing,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getResourceBilling(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	instanceID := vars["instance_id"]

	startDate := r.URL.Query().Get("start_date")
	endDate := r.URL.Query().Get("end_date")

	if startDate == "" || endDate == "" {
		now := time.Now()
		firstDay := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
		lastDay := time.Date(now.Year(), now.Month(), 0, 23, 59, 59, 0, time.UTC)
		startDate = firstDay.Format("2006-01-02T15:04:05")
		endDate = lastDay.Format("2006-01-02T15:04:05")
	}

	config := GnocchiConfig{
		BaseURL:  getEnv("GNOCCHI_URL", ""),
		Token:    getEnv("GNOCCHI_TOKEN", ""),
		Insecure: true,
	}

	client := NewGnocchiClient(config)

	// Get instance resource
	instance, err := client.GetInstanceResource(instanceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get instance: %v", err), http.StatusInternalServerError)
		return
	}

	// Get all resource metrics
	resourceUsage := ResourceUsage{
		InstanceID:   instanceID,
		InstanceName: instance.DisplayName,
		StartDate:    startDate,
		EndDate:      endDate,
		FlavorName:   instance.FlavorName,
	}

	// CPU
	if cpuMetricID, ok := instance.Metrics["cpu"]; ok {
		measures, _ := client.GetMetricMeasures(cpuMetricID, startDate, endDate, 300)
		numVCPUs := 2
		if vcpuMetricID, ok := instance.Metrics["vcpus"]; ok {
			vcpuMeasures, _ := client.GetMetricMeasures(vcpuMetricID, startDate, endDate, 3600)
			if len(vcpuMeasures) > 0 {
				numVCPUs = int(vcpuMeasures[0].Value)
			}
		}
		cpuUsage := CalculateCPUUsage(measures, numVCPUs)
		resourceUsage.CPU = cpuUsage
		resourceUsage.VCPUs = numVCPUs
	}

	// Memory
	if memUsageMetricID, ok := instance.Metrics["memory.usage"]; ok {
		memMeasures, _ := client.GetMetricMeasures(memUsageMetricID, startDate, endDate, 3600)
		if memTotalMetricID, ok := instance.Metrics["memory"]; ok {
			memTotalMeasures, _ := client.GetMetricMeasures(memTotalMetricID, startDate, endDate, 3600)
			if len(memTotalMeasures) > 0 {
				memUsage := CalculateMemoryUsage(memMeasures, memTotalMeasures)
				resourceUsage.Memory = memUsage
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resourceUsage)
}

func getBillingReport(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	instanceID := vars["instance_id"]

	startDate := r.URL.Query().Get("start_date")
	endDate := r.URL.Query().Get("end_date")

	// Pricing from query params or use default
	cpuPricePerHour := parseFloat(r.URL.Query().Get("cpu_price_per_hour"), 0.05)
	memoryPricePerGB := parseFloat(r.URL.Query().Get("memory_price_per_gb"), 0.01)

	if startDate == "" || endDate == "" {
		now := time.Now()
		firstDay := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
		lastDay := time.Date(now.Year(), now.Month(), 0, 23, 59, 59, 0, time.UTC)
		startDate = firstDay.Format("2006-01-02T15:04:05")
		endDate = lastDay.Format("2006-01-02T15:04:05")
	}

	config := GnocchiConfig{
		BaseURL:  getEnv("GNOCCHI_URL", ""),
		Token:    getEnv("GNOCCHI_TOKEN", ""),
		Insecure: true,
	}

	client := NewGnocchiClient(config)
	instance, err := client.GetInstanceResource(instanceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get instance: %v", err), http.StatusInternalServerError)
		return
	}

	report := BillingReport{
		InstanceID:       instanceID,
		InstanceName:     instance.DisplayName,
		FlavorName:       instance.FlavorName,
		StartDate:        startDate,
		EndDate:          endDate,
		GeneratedAt:      time.Now().Format(time.RFC3339),
		Currency:         "USD",
		CPUPricePerHour:  cpuPricePerHour,
		MemoryPricePerGB: memoryPricePerGB,
	}

	// Calculate CPU billing
	if cpuMetricID, ok := instance.Metrics["cpu"]; ok {
		measures, _ := client.GetMetricMeasures(cpuMetricID, startDate, endDate, 300)
		numVCPUs := 2
		if vcpuMetricID, ok := instance.Metrics["vcpus"]; ok {
			vcpuMeasures, _ := client.GetMetricMeasures(vcpuMetricID, startDate, endDate, 300)
			if len(vcpuMeasures) > 0 {
				numVCPUs = int(vcpuMeasures[0].Value)
			}
		}
		cpuUsage := CalculateCPUUsage(measures, numVCPUs)
		cpuBilling := CalculateCPUBilling(cpuUsage, startDate, endDate)

		report.CPUUsage = cpuUsage
		report.VCPUs = numVCPUs
		report.CPUCost = cpuBilling.TotalCPUHours * cpuPricePerHour
	}

	// Calculate Memory billing
	if memUsageMetricID, ok := instance.Metrics["memory.usage"]; ok {
		memMeasures, _ := client.GetMetricMeasures(memUsageMetricID, startDate, endDate, 300)
		if memTotalMetricID, ok := instance.Metrics["memory"]; ok {
			memTotalMeasures, _ := client.GetMetricMeasures(memTotalMetricID, startDate, endDate, 300)
			if len(memTotalMeasures) > 0 {
				memUsage := CalculateMemoryUsage(memMeasures, memTotalMeasures)
				report.MemoryUsage = memUsage

				// Calculate memory cost based on GB-hours
				totalMemoryGB := memUsage.AverageUsedMB / 1024.0
				start, _ := time.Parse("2006-01-02T15:04:05", startDate)
				end, _ := time.Parse("2006-01-02T15:04:05", endDate)
				totalHours := end.Sub(start).Hours()
				report.MemoryCost = totalMemoryGB * totalHours * memoryPricePerGB
			}
		}
	}

	report.TotalCost = report.CPUCost + report.MemoryCost

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}
