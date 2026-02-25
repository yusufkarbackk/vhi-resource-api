package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
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

	// Provisioned storage (from VHI panel stat)
	ProvisionedStorageTiB float64 `json:"provisioned_storage_tib"`
	StorageUsedTiB        float64 `json:"storage_used_tib"`
	StorageFreeTiB        float64 `json:"storage_free_tib"`

	StorageError string `json:"storage_error,omitempty"`
}

// GET /api/v1/usage/cluster
func getClusterUsage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	adminToken, err := GetAdminToken(ctx)
	if err != nil {
		log.Printf("Error: failed to get admin token: %v", err)
		http.Error(w, fmt.Sprintf("failed to authenticate admin: %v", err), http.StatusUnauthorized)
		return
	}

	// ---- Try VHI Panel stat (primary source, exact dashboard data) ----
	panelURL := getEnv("VHI_PANEL_URL", "")
	var response ClusterUsage

	if panelURL != "" {
		panelClient := NewVHIPanelClient(VHIPanelConfig{
			BaseURL:  panelURL,
			Username: getEnv("ADMIN_USERNAME", "admin"),
			Password: getEnv("ADMIN_PASSWORD", ""),
			Domain:   getEnv("ADMIN_DOMAIN_NAME", "Default"),
			Insecure: true,
		})

		stat, panelErr := panelClient.GetStat()
		if panelErr != nil {
			log.Printf("Warning: VHI Panel stat failed: %v, falling back to Nova", panelErr)
		} else {
			// Panel stat available - use exact dashboard data
			bytesToGiB := 1024.0 * 1024.0 * 1024.0
			bytesToTiB := bytesToGiB * 1024.0

			response = ClusterUsage{
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

				ProvisionedStorageTiB: math.Ceil(float64(stat.Compute.BlockCapacity)/bytesToTiB*100) / 100,
				StorageUsedTiB:        math.Ceil(float64(stat.Compute.BlockUsage)/bytesToTiB*100) / 100,
				StorageFreeTiB:        math.Ceil(float64(stat.Compute.BlockCapacity-stat.Compute.BlockUsage)/bytesToTiB*100) / 100,
			}

			log.Printf("Using VHI Panel stat: Total=%d vCPUs | System=%d | VMs=%d | Free=%d | Fenced=%d | Storage=%.2f TiB",
				response.TotalVCPUs, response.SystemVCPUs, response.ReservedVCPUs,
				response.FreeVCPUs, response.FencedVCPUs, response.ProvisionedStorageTiB)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
	}

	// ---- Fallback: Nova + Gnocchi calculations ----
	log.Printf("Using fallback: Nova hypervisors + Gnocchi/Cinder")

	novaURL := getEnv("NOVA_URL", "")
	novaClient := NewNovaClient(NovaConfig{
		BaseURL:  novaURL,
		Token:    adminToken,
		Insecure: true,
	})

	hypervisors, err := novaClient.GetHypervisors()
	if err != nil {
		log.Printf("Error: failed to get hypervisors: %v", err)
		http.Error(w, fmt.Sprintf("failed to get hypervisors: %v", err), http.StatusInternalServerError)
		return
	}

	overcommitStr := getEnv("OVERCOMMIT_RATIO", "8")
	vCPUOvercommit, err := strconv.ParseFloat(overcommitStr, 64)
	if err != nil {
		vCPUOvercommit = 8.0
	}
	ramOvercommit := 1.0

	var physicalVCPUs, fencedPhysicalVCPUs, activePhysicalVCPUs int
	var physicalRAMMB, fencedPhysicalRAMMB, activePhysicalRAMMB int
	var activeFreeRAMMB int
	var activeVCPUsUsed, activeRAMMBUsed int

	for _, hyp := range hypervisors {
		physicalVCPUs += hyp.VCPUs
		physicalRAMMB += hyp.MemoryMB

		if hyp.State == "down" || hyp.Status == "disabled" {
			fencedPhysicalVCPUs += hyp.VCPUs
			fencedPhysicalRAMMB += hyp.MemoryMB
		} else {
			activePhysicalVCPUs += hyp.VCPUs
			activePhysicalRAMMB += hyp.MemoryMB
			activeFreeRAMMB += hyp.FreeRAMMB
			activeVCPUsUsed += hyp.VCPUsUsed
			activeRAMMBUsed += hyp.MemoryMBUsed
		}
	}

	totalVCPUs := int(float64(physicalVCPUs) * vCPUOvercommit)
	totalRAMGiB := (float64(physicalRAMMB) / 1024.0) * ramOvercommit
	fencedVCPUs := int(float64(fencedPhysicalVCPUs) * vCPUOvercommit)
	fencedRAMGiB := (float64(fencedPhysicalRAMMB) / 1024.0) * ramOvercommit
	activeTotalVCPUs := int(float64(activePhysicalVCPUs) * vCPUOvercommit)
	activeTotalRAMGiB := (float64(activePhysicalRAMMB) / 1024.0) * ramOvercommit

	// Gnocchi provisioned storage
	gnocchiURL := getEnv("GNOCCHI_URL", "")
	var provisionedTiB float64
	if gnocchiURL != "" {
		gnocchiClient := NewGnocchiClient(GnocchiConfig{
			BaseURL:  gnocchiURL,
			Token:    adminToken,
			Insecure: true,
		})
		gnocchiStorage, gnocchiErr := gnocchiClient.GetProvisionedStorage()
		if gnocchiErr != nil {
			log.Printf("Warning: Gnocchi failed: %v", gnocchiErr)
		} else {
			provisionedTiB = gnocchiStorage.TotalTiB
		}
	}

	// Nova servers
	servers, err := novaClient.ListAllServers()
	if err != nil {
		log.Printf("Error: failed to list servers from Nova: %v", err)
		http.Error(w, fmt.Sprintf("failed to list servers from Nova: %v", err), http.StatusInternalServerError)
		return
	}

	var reservedVCPUs, reservedRAMMB int
	var activeVMs, shutoffVMs, shelvedVMs, otherVMs int

	for _, server := range servers {
		switch server.Status {
		case "ACTIVE":
			activeVMs++
			reservedVCPUs += server.Flavor.VCPUs
			reservedRAMMB += server.Flavor.RAM
		case "SHUTOFF":
			shutoffVMs++
		case "SHELVED_OFFLOADED", "SHELVED":
			shelvedVMs++
		default:
			otherVMs++
		}
	}

	reservedRAMGiB := float64(reservedRAMMB) / 1024.0
	freeRAMGiB := float64(activeFreeRAMMB) / 1024.0

	systemRAMGiB := (float64(activeRAMMBUsed) / 1024.0) - reservedRAMGiB
	if systemRAMGiB < 0 {
		systemRAMGiB = 0
	}

	freeRatio := 0.0
	if activeTotalRAMGiB > 0 {
		freeRatio = freeRAMGiB / activeTotalRAMGiB
	}
	freeVCPUs := int(freeRatio * float64(activeTotalVCPUs))

	systemVCPUs := activeTotalVCPUs - freeVCPUs - reservedVCPUs
	if systemVCPUs < 0 {
		systemVCPUs = 0
	}

	response = ClusterUsage{
		Timestamp:      time.Now().Format(time.RFC3339),
		TotalVMs:       len(servers),
		ActiveVMs:      activeVMs,
		ShutoffVMs:     shutoffVMs,
		ShelvedVMs:     shelvedVMs,
		OtherVMs:       otherVMs,
		TotalVCPUs:     totalVCPUs,
		TotalRAMTiB:    math.Ceil(totalRAMGiB/1024.0*100) / 100,
		ReservedVCPUs:  reservedVCPUs,
		ReservedRAMGiB: math.Ceil(reservedRAMGiB),
		FencedVCPUs:    fencedVCPUs,
		FencedRAMGiB:   math.Ceil(fencedRAMGiB),
		SystemVCPUs:    systemVCPUs,
		SystemRAMGiB:   math.Ceil(systemRAMGiB),
		FreeVCPUs:      freeVCPUs,
		FreeRAMGiB:     math.Ceil(freeRAMGiB),

		ProvisionedStorageTiB: provisionedTiB,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
