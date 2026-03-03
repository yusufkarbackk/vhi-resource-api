package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
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

	// Cluster capacity (sum of individual hypervisors)
	TotalVCPUs  int     `json:"total_vcpus"`
	TotalRAMTiB float64 `json:"total_ram_tib"`

	// Fenced capacity (nodes that are down)
	FencedVCPUs  int     `json:"fenced_vcpus"`
	FencedRAMGiB float64 `json:"fenced_ram_gib"`

	// Reserved = resources on hypervisor (Active + Shutoff only)
	ReservedVCPUs  int     `json:"reserved_vcpus"`
	ReservedRAMGiB float64 `json:"reserved_ram_gib"`

	// System = hypervisor/system overhead
	SystemVCPUs  int     `json:"system_vcpus"`
	SystemRAMGiB float64 `json:"system_ram_gib"`

	// Free = Total - Used
	FreeVCPUs  int     `json:"free_vcpus"`
	FreeRAMGiB float64 `json:"free_ram_gib"`

	// Logical storage (vstorage cluster — matches vstorage CLI: 287TB of 377TB)
	LogicalStorageTotalTiB float64 `json:"logical_storage_total_tib"`
	LogicalStorageUsedTiB  float64 `json:"logical_storage_used_tib"`
	LogicalStorageFreeTiB  float64 `json:"logical_storage_free_tib"`

	StorageError string `json:"storage_error,omitempty"`
}

// GET /api/v1/usage/cluster
func getClusterUsage(w http.ResponseWriter, r *http.Request) {
	// ---- Check Redis cache first ----
	if cached := getCachedClusterUsage(); cached != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached)
		return
	}

	// ---- VHI Panel stat (only source) ----

	if panelClient == nil {
		log.Printf("Error: VHI Panel client not initialized")
		http.Error(w, `{"error":"VHI Panel client not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	// Run GetStat() and GetStorageStat() in parallel
	var (
		stat        *PanelStat
		panelErr    error
		storageStat *VStorageStat
		storageErr  error
		wg          sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		stat, panelErr = panelClient.GetStat()
	}()

	go func() {
		defer wg.Done()
		storageStat, storageErr = panelClient.GetStorageStat()
	}()

	wg.Wait()

	if panelErr != nil {
		log.Printf("Error: VHI Panel stat failed: %v", panelErr)
		http.Error(w, fmt.Sprintf(`{"error":"VHI Panel stat failed: %v"}`, panelErr), http.StatusBadGateway)
		return
	}

	// Panel stat available - use exact dashboard data
	bytesToGiB := 1024.0 * 1024.0 * 1024.0
	bytesToTiB := bytesToGiB * 1024.0

	response := ClusterUsage{
		Timestamp:  time.Now().Format(time.RFC3339),
		TotalVMs:   stat.Servers.Count,
		ActiveVMs:  stat.Servers.Active,
		ShutoffVMs: stat.Servers.Shutoff,
		ShelvedVMs: stat.Servers.ShelvedOffloaded,
		OtherVMs:   stat.Servers.Error + stat.Servers.InProgress,

		TotalVCPUs:  stat.Physical.VCPUsTotal,
		TotalRAMTiB: math.Ceil(float64(stat.Physical.MemTotal)/bytesToTiB*100) / 100,

		FencedVCPUs:  stat.Fenced.VCPUs,
		FencedRAMGiB: math.Ceil(float64(stat.Fenced.PhysicalMemTotal) / bytesToGiB),

		ReservedVCPUs:  stat.Compute.VCPUs,
		ReservedRAMGiB: math.Ceil(float64(stat.Compute.VmMemReserved) / bytesToGiB),

		SystemVCPUs:  stat.Reserved.VCPUs,
		SystemRAMGiB: math.Ceil(float64(stat.Reserved.Memory) / bytesToGiB),

		FreeVCPUs:  stat.Compute.VCPUsFree,
		FreeRAMGiB: math.Ceil(float64(stat.Compute.VmMemFree) / bytesToGiB),
	}

	// Attach logical storage from parallel GetStorageStat()
	if storageErr != nil {
		log.Printf("Warning: VHI Panel storage stat failed: %v", storageErr)
		response.StorageError = storageErr.Error()
	} else {
		response.LogicalStorageTotalTiB = math.Round(storageStat.TotalBytes/bytesToTiB*100) / 100
		response.LogicalStorageUsedTiB = math.Round(storageStat.UsedBytes/bytesToTiB*100) / 100
		response.LogicalStorageFreeTiB = math.Round(storageStat.FreeBytes/bytesToTiB*100) / 100
	}

	log.Printf("Using VHI Panel stat: Total=%d vCPUs | System=%d | VMs=%d | Free=%d | Fenced=%d",
		response.TotalVCPUs, response.SystemVCPUs, response.ReservedVCPUs,
		response.FreeVCPUs, response.FencedVCPUs)

	// Store in Redis cache
	setCachedClusterUsage(&response)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
