package main

import (
	"os"
	"strconv"
)

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func parseFloat(s string, defaultValue float64) float64 {
	if s == "" {
		return defaultValue
	}
	
	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return defaultValue
	}
	
	return value
}
