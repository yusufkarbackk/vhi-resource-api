package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// ClusterUsage merepresentasikan total resource usage untuk seluruh cluster.
type ClusterUsage struct {
	Timestamp string `json:"timestamp"`

	// VM counts
	TotalVMs   int `json:"total_vms"`
	ActiveVMs  int `json:"active_vms"`
	ShutoffVMs int `json:"shutoff_vms"`
	ShelvedVMs int `json:"shelved_vms"`
	OtherVMs   int `json:"other_vms"`

	// Cluster capacity (from hypervisor stats)
	TotalVCPUs  int     `json:"total_vcpus"`   // Total vCPUs capacity (physical * overcommit ratio)
	TotalRAMGiB float64 `json:"total_ram_gib"` // Total RAM capacity

	// Reserved = resources actually occupying hypervisor (Active + Shutoff only)
	ReservedVCPUs  int     `json:"reserved_vcpus"`
	ReservedRAMGiB float64 `json:"reserved_ram_gib"`

	// System = used by hypervisor/system overhead
	SystemVCPUs  int     `json:"system_vcpus"`
	SystemRAMGiB float64 `json:"system_ram_gib"`

	// Free = Total - System - Reserved
	FreeVCPUs  int     `json:"free_vcpus"`
	FreeRAMGiB float64 `json:"free_ram_gib"`
}

// GET /api/v1/usage/cluster
// Mendapatkan total resource usage dari SELURUH cluster menggunakan Nova Compute API.
func getClusterUsage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	// 1. Login admin ke Keystone
	adminToken, err := GetAdminToken(ctx)
	if err != nil {
		log.Printf("Error: failed to get admin token: %v", err)
		http.Error(w, fmt.Sprintf("failed to authenticate admin: %v", err), http.StatusUnauthorized)
		return
	}

	// 2. Buat Nova client
	novaURL := getEnv("NOVA_URL", "")
	novaClient := NewNovaClient(NovaConfig{
		BaseURL:  novaURL,
		Token:    adminToken,
		Insecure: true,
	})

	// 3. Ambil hypervisor statistics (cluster capacity)
	log.Println("Fetching hypervisor statistics...")
	hyperStats, err := novaClient.GetHypervisorStats()
	if err != nil {
		log.Printf("Error: failed to get hypervisor stats: %v", err)
		http.Error(w, fmt.Sprintf("failed to get hypervisor stats: %v", err), http.StatusInternalServerError)
		return
	}

	totalVCPUs := hyperStats.VCPUs
	totalRAMGiB := float64(hyperStats.MemoryMB) / 1024.0

	log.Printf("Hypervisor capacity: %d vCPUs, %.2f GiB RAM (%d hypervisors)", totalVCPUs, totalRAMGiB, hyperStats.Count)

	// 4. Ambil semua servers dari Nova
	log.Println("Fetching all servers from Nova (all_tenants=true)...")
	servers, err := novaClient.ListAllServers()
	if err != nil {
		log.Printf("Error: failed to list servers from Nova: %v", err)
		http.Error(w, fmt.Sprintf("failed to list servers from Nova: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Found %d total servers in cluster", len(servers))

	// 5. Hitung reserved resource (Active + Shutoff only)
	var reservedVCPUs int
	var reservedRAMMB int
	var activeVMs, shutoffVMs, shelvedVMs, otherVMs int

	for _, server := range servers {
		switch server.Status {
		case "ACTIVE":
			activeVMs++
			reservedVCPUs += server.Flavor.VCPUs
			reservedRAMMB += server.Flavor.RAM
		case "SHUTOFF":
			shutoffVMs++
			reservedVCPUs += server.Flavor.VCPUs
			reservedRAMMB += server.Flavor.RAM
		case "SHELVED_OFFLOADED", "SHELVED":
			shelvedVMs++
		default:
			otherVMs++
			reservedVCPUs += server.Flavor.VCPUs
			reservedRAMMB += server.Flavor.RAM
		}
	}

	reservedRAMGiB := float64(reservedRAMMB) / 1024.0

	// 6. Hitung system dan free
	// System = hypervisor used - VM reserved (overhead dari OS/hypervisor)
	systemVCPUs := hyperStats.VCPUsUsed - reservedVCPUs
	if systemVCPUs < 0 {
		systemVCPUs = 0
	}
	systemRAMMB := hyperStats.MemoryMBUsed - reservedRAMMB
	if systemRAMMB < 0 {
		systemRAMMB = 0
	}
	systemRAMGiB := float64(systemRAMMB) / 1024.0

	// Free = Total - Used (used includes system + VMs)
	freeVCPUs := totalVCPUs - hyperStats.VCPUsUsed
	if freeVCPUs < 0 {
		freeVCPUs = 0
	}
	freeRAMGiB := float64(hyperStats.FreeRAMMB) / 1024.0

	log.Printf("========================================")
	log.Printf("Cluster: %d vCPUs total, %d hypervisors", totalVCPUs, hyperStats.Count)
	log.Printf("  System: %d vCPUs, %.2f GiB RAM", systemVCPUs, systemRAMGiB)
	log.Printf("  VMs:    %d vCPUs, %.2f GiB RAM", reservedVCPUs, reservedRAMGiB)
	log.Printf("  Free:   %d vCPUs, %.2f GiB RAM", freeVCPUs, freeRAMGiB)
	log.Printf("  VMs: %d total (Active: %d, Shutoff: %d, Shelved: %d, Other: %d)",
		len(servers), activeVMs, shutoffVMs, shelvedVMs, otherVMs)
	log.Printf("========================================")

	// 7. Return response
	response := ClusterUsage{
		Timestamp:      time.Now().Format(time.RFC3339),
		TotalVMs:       len(servers),
		ActiveVMs:      activeVMs,
		ShutoffVMs:     shutoffVMs,
		ShelvedVMs:     shelvedVMs,
		OtherVMs:       otherVMs,
		TotalVCPUs:     totalVCPUs,
		TotalRAMGiB:    totalRAMGiB,
		ReservedVCPUs:  reservedVCPUs,
		ReservedRAMGiB: reservedRAMGiB,
		SystemVCPUs:    systemVCPUs,
		SystemRAMGiB:   systemRAMGiB,
		FreeVCPUs:      freeVCPUs,
		FreeRAMGiB:     freeRAMGiB,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
