package main

import (
	"log"
	"math"
	"time"
)

type CPUUsageStats struct {
	TotalDataPoints int           `json:"total_data_points"`
	AveragePercent  float64       `json:"average_percent"`
	MaxPercent      float64       `json:"max_percent"`
	MinPercent      float64       `json:"min_percent"`
	MedianPercent   float64       `json:"median_percent"`
	Percentile95    float64       `json:"percentile_95"`
	UsageByHour     []HourlyUsage `json:"usage_by_hour"`
	UsageByDay      []DailyUsage  `json:"usage_by_day"`
}

type HourlyUsage struct {
	Timestamp  string  `json:"timestamp"`
	CPUPercent float64 `json:"cpu_percent"`
	CPUSeconds float64 `json:"cpu_seconds"`
}

type DailyUsage struct {
	Date          string  `json:"date"`
	AverageCPU    float64 `json:"average_cpu_percent"`
	MaxCPU        float64 `json:"max_cpu_percent"`
	MinCPU        float64 `json:"min_cpu_percent"`
	TotalCPUHours float64 `json:"total_cpu_hours"`
}

type CPUBillingInfo struct {
	TotalCPUHours      float64 `json:"total_cpu_hours"`
	TotalCPUCoreHours  float64 `json:"total_cpu_core_hours"`
	AverageCPUPercent  float64 `json:"average_cpu_percent"`
	BillingPeriodDays  int     `json:"billing_period_days"`
	BillingPeriodHours float64 `json:"billing_period_hours"`
}

type MemoryUsageStats struct {
	AverageUsedMB  float64         `json:"average_used_mb"`
	AverageUsedGB  float64         `json:"average_used_gb"`
	MaxUsedMB      float64         `json:"max_used_mb"`
	MinUsedMB      float64         `json:"min_used_mb"`
	AveragePercent float64         `json:"average_percent"`
	TotalMemoryMB  float64         `json:"total_memory_mb"`
	UsageByDay     []DailyMemUsage `json:"usage_by_day"`
}

type DailyMemUsage struct {
	Date           string  `json:"date"`
	AverageUsedMB  float64 `json:"average_used_mb"`
	AveragePercent float64 `json:"average_percent"`
}

type CPUBillingResponse struct {
	InstanceID   string         `json:"instance_id"`
	InstanceName string         `json:"instance_name"`
	StartDate    string         `json:"start_date"`
	EndDate      string         `json:"end_date"`
	VCPUs        int            `json:"vcpus"`
	Usage        CPUUsageStats  `json:"usage"`
	Billing      CPUBillingInfo `json:"billing"`
}

type ResourceUsage struct {
	InstanceID   string           `json:"instance_id"`
	InstanceName string           `json:"instance_name"`
	FlavorName   string           `json:"flavor_name"`
	StartDate    string           `json:"start_date"`
	EndDate      string           `json:"end_date"`
	VCPUs        int              `json:"vcpus"`
	CPU          CPUUsageStats    `json:"cpu"`
	Memory       MemoryUsageStats `json:"memory"`
}

type BillingReport struct {
	InstanceID       string           `json:"instance_id"`
	InstanceName     string           `json:"instance_name"`
	FlavorName       string           `json:"flavor_name"`
	StartDate        string           `json:"start_date"`
	EndDate          string           `json:"end_date"`
	GeneratedAt      string           `json:"generated_at"`
	Currency         string           `json:"currency"`
	VCPUs            int              `json:"vcpus"`
	CPUUsage         CPUUsageStats    `json:"cpu_usage"`
	MemoryUsage      MemoryUsageStats `json:"memory_usage"`
	CPUPricePerHour  float64          `json:"cpu_price_per_hour"`
	MemoryPricePerGB float64          `json:"memory_price_per_gb_hour"`
	CPUCost          float64          `json:"cpu_cost"`
	MemoryCost       float64          `json:"memory_cost"`
	TotalCost        float64          `json:"total_cost"`
}

func CalculateCPUUsage(measures []MetricMeasure, numVCPUs int) CPUUsageStats {
	if len(measures) < 2 {
		log.Printf("Warning: Not enough measures (%d), need at least 2", len(measures))
		return CPUUsageStats{}
	}

	if numVCPUs <= 0 {
		log.Printf("Warning: Invalid numVCPUs (%d), defaulting to 1", numVCPUs)
		numVCPUs = 1
	}

	var hourlyUsages []HourlyUsage
	var percentages []float64
	dailyUsageMap := make(map[string]*DailyUsage)

	skippedNegative := 0
	skippedAbnormal := 0
	totalProcessed := 0

	for i := 1; i < len(measures); i++ {
		prev := measures[i-1]
		curr := measures[i]

		// Calculate delta CPU time in nanoseconds
		deltaCPU := curr.Value - prev.Value

		// CRITICAL: Skip negative delta (VM restart, live migration, or counter reset)
		if deltaCPU < 0 {
			skippedNegative++
			log.Printf("Warning: Negative CPU delta (%.2f ns) at %s - likely VM restart/migration, skipping",
				deltaCPU, curr.Timestamp)
			continue
		}

		// Calculate time delta in seconds
		timePrev, _ := time.Parse(time.RFC3339, prev.Timestamp)
		timeCurr, _ := time.Parse(time.RFC3339, curr.Timestamp)
		deltaTime := timeCurr.Sub(timePrev).Seconds()

		// Skip if time delta is invalid
		if deltaTime <= 0 {
			skippedAbnormal++
			log.Printf("Warning: Invalid time delta (%.2f s) at %s, skipping", deltaTime, curr.Timestamp)
			continue
		}

		// Calculate CPU percentage using the correct formula:
		// CPU% = (delta_cpu_nanoseconds / (delta_time_seconds * num_vcpus * 1e9)) * 100
		cpuPercent := (deltaCPU / (deltaTime * float64(numVCPUs) * 1e9)) * 100

		// Validate: CPU percentage should be between 0 and 100 per vCPU
		// For multi-core, max is 100% (not 100% * numVCPUs)
		maxAllowed := 100.0
		if cpuPercent < 0 || cpuPercent > maxAllowed*1.1 { // Allow 10% margin for measurement error
			skippedAbnormal++
			log.Printf("Warning: Abnormal CPU%% (%.2f%%) at %s (delta: %.2f ns, time: %.2f s), skipping",
				cpuPercent, curr.Timestamp, deltaCPU, deltaTime)
			continue
		}

		// CPU seconds used (actual compute time)
		cpuSeconds := deltaCPU / 1e9

		// Valid data point - add to results
		totalProcessed++

		hourlyUsages = append(hourlyUsages, HourlyUsage{
			Timestamp:  curr.Timestamp,
			CPUPercent: cpuPercent,
			CPUSeconds: cpuSeconds,
		})

		percentages = append(percentages, cpuPercent)

		// Aggregate by day
		dateKey := timeCurr.Format("2006-01-02")

		if _, exists := dailyUsageMap[dateKey]; !exists {
			dailyUsageMap[dateKey] = &DailyUsage{
				Date:   dateKey,
				MinCPU: cpuPercent,
				MaxCPU: cpuPercent,
			}
		}

		daily := dailyUsageMap[dateKey]
		daily.AverageCPU += cpuPercent
		daily.TotalCPUHours += cpuSeconds / 3600.0

		if cpuPercent > daily.MaxCPU {
			daily.MaxCPU = cpuPercent
		}
		if cpuPercent < daily.MinCPU {
			daily.MinCPU = cpuPercent
		}
	}

	// Log summary of data quality
	totalMeasures := len(measures) - 1
	log.Printf("CPU Usage Calculation Summary:")
	log.Printf("  Total intervals: %d", totalMeasures)
	log.Printf("  Valid data points: %d (%.1f%%)", totalProcessed, float64(totalProcessed)/float64(totalMeasures)*100)
	log.Printf("  Skipped negative: %d", skippedNegative)
	log.Printf("  Skipped abnormal: %d", skippedAbnormal)

	// Convert daily map to slice and calculate averages
	var dailyUsages []DailyUsage
	for _, daily := range dailyUsageMap {
		// Calculate average CPU per day by dividing by number of data points for that day
		dataPointsThisDay := 0
		for _, usage := range hourlyUsages {
			t, _ := time.Parse(time.RFC3339, usage.Timestamp)
			if t.Format("2006-01-02") == daily.Date {
				dataPointsThisDay++
			}
		}

		if dataPointsThisDay > 0 {
			daily.AverageCPU = daily.AverageCPU / float64(dataPointsThisDay)
		}
		dailyUsages = append(dailyUsages, *daily)
	}

	// Calculate statistics
	stats := CPUUsageStats{
		TotalDataPoints: len(percentages),
		UsageByHour:     hourlyUsages,
		UsageByDay:      dailyUsages,
	}

	if len(percentages) > 0 {
		stats.AveragePercent = average(percentages)
		stats.MaxPercent = max(percentages)
		stats.MinPercent = min(percentages)
		stats.MedianPercent = median(percentages)
		stats.Percentile95 = percentile(percentages, 95)

		log.Printf("CPU Statistics:")
		log.Printf("  Average: %.2f%%", stats.AveragePercent)
		log.Printf("  Median: %.2f%%", stats.MedianPercent)
		log.Printf("  95th percentile: %.2f%%", stats.Percentile95)
		log.Printf("  Min: %.2f%%, Max: %.2f%%", stats.MinPercent, stats.MaxPercent)
	} else {
		log.Printf("Warning: No valid CPU data points after filtering")
	}

	return stats
}

func CalculateCPUBilling(usage CPUUsageStats, startDate, endDate string) CPUBillingInfo {
	start, _ := time.Parse("2006-01-02T15:04:05", startDate)
	end, _ := time.Parse("2006-01-02T15:04:05", endDate)

	totalHours := end.Sub(start).Hours()
	totalDays := int(math.Ceil(totalHours / 24.0))

	// Calculate total CPU hours from daily usage
	var totalCPUHours float64
	for _, daily := range usage.UsageByDay {
		totalCPUHours += daily.TotalCPUHours
	}

	return CPUBillingInfo{
		TotalCPUHours:      totalCPUHours,
		TotalCPUCoreHours:  totalCPUHours, // Already calculated per core
		AverageCPUPercent:  usage.AveragePercent,
		BillingPeriodDays:  totalDays,
		BillingPeriodHours: totalHours,
	}
}

func CalculateMemoryUsage(usageMeasures, totalMeasures []MetricMeasure) MemoryUsageStats {
	if len(usageMeasures) == 0 || len(totalMeasures) == 0 {
		return MemoryUsageStats{}
	}

	var usedMBs []float64
	var percentages []float64
	dailyUsageMap := make(map[string]*DailyMemUsage)

	totalMemoryMB := totalMeasures[0].Value

	for i, usageMeasure := range usageMeasures {
		usedMB := usageMeasure.Value
		usedMBs = append(usedMBs, usedMB)

		percent := (usedMB / totalMemoryMB) * 100
		percentages = append(percentages, percent)

		// Aggregate by day
		t, _ := time.Parse(time.RFC3339, usageMeasure.Timestamp)
		dateKey := t.Format("2006-01-02")

		if _, exists := dailyUsageMap[dateKey]; !exists {
			dailyUsageMap[dateKey] = &DailyMemUsage{
				Date: dateKey,
			}
		}

		daily := dailyUsageMap[dateKey]
		daily.AverageUsedMB += usedMB
		daily.AveragePercent += percent

		// Simple counter for averaging
		if i == len(usageMeasures)-1 ||
			(i+1 < len(usageMeasures) &&
				usageMeasures[i+1].Timestamp[:10] != dateKey) {
			// End of day, calculate average
			// This is simplified - in production you'd track counts properly
		}
	}

	// Convert daily map to slice
	var dailyUsages []DailyMemUsage
	dataPointsPerDay := len(usageMeasures) / len(dailyUsageMap)
	if dataPointsPerDay == 0 {
		dataPointsPerDay = 1
	}

	for _, daily := range dailyUsageMap {
		daily.AverageUsedMB = daily.AverageUsedMB / float64(dataPointsPerDay)
		daily.AveragePercent = daily.AveragePercent / float64(dataPointsPerDay)
		dailyUsages = append(dailyUsages, *daily)
	}

	stats := MemoryUsageStats{
		TotalMemoryMB: totalMemoryMB,
		UsageByDay:    dailyUsages,
	}

	if len(usedMBs) > 0 {
		stats.AverageUsedMB = average(usedMBs)
		stats.AverageUsedGB = stats.AverageUsedMB / 1024.0
		stats.MaxUsedMB = max(usedMBs)
		stats.MinUsedMB = min(usedMBs)
	}

	if len(percentages) > 0 {
		stats.AveragePercent = average(percentages)
	}

	return stats
}

// Helper functions
func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func max(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	maxVal := values[0]
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal
}

func min(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	minVal := values[0]
	for _, v := range values {
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Simple median calculation (should sort in production)
	sorted := make([]float64, len(values))
	copy(sorted, values)

	// Bubble sort (use proper sort in production)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)

	// Bubble sort
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	index := int(float64(len(sorted)) * p / 100.0)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
