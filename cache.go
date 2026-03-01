package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisClient is the global Redis client, initialized once at startup.
var redisClient *redis.Client

// cacheKey is the Redis key for the cluster usage cache.
const cacheKey = "vhi:cluster_usage"

// initRedis initializes the Redis client from environment variables.
// Env vars: REDIS_HOST, REDIS_PORT, REDIS_PASSWORD, REDIS_DB
// Returns nil if REDIS_HOST is not set (caching disabled).
func initRedis() *redis.Client {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		log.Println("REDIS_HOST not set — caching disabled")
		return nil
	}

	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}

	password := os.Getenv("REDIS_PASSWORD")

	db := 0
	if dbStr := os.Getenv("REDIS_DB"); dbStr != "" {
		if parsed, err := strconv.Atoi(dbStr); err == nil {
			db = parsed
		}
	}

	addr := fmt.Sprintf("%s:%s", host, port)
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		log.Printf("Warning: Redis connection failed (%s): %v — caching disabled", addr, err)
		return nil
	}

	log.Printf("Redis connected: %s (db=%d)", addr, db)
	return client
}

// getCacheTTL returns the cache TTL from env (default 60 seconds).
func getCacheTTL() time.Duration {
	ttlStr := os.Getenv("CACHE_TTL_SECONDS")
	if ttlStr == "" {
		return 60 * time.Second
	}
	ttl, err := strconv.Atoi(ttlStr)
	if err != nil || ttl <= 0 {
		return 60 * time.Second
	}
	return time.Duration(ttl) * time.Second
}

// getCachedClusterUsage tries to get a cached ClusterUsage from Redis.
// Returns nil if cache miss or Redis unavailable.
func getCachedClusterUsage() *ClusterUsage {
	if redisClient == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	data, err := redisClient.Get(ctx, cacheKey).Bytes()
	if err != nil {
		// Cache miss or error — not a problem
		return nil
	}

	var usage ClusterUsage
	if err := json.Unmarshal(data, &usage); err != nil {
		log.Printf("Warning: failed to unmarshal cached cluster usage: %v", err)
		return nil
	}

	log.Printf("Cache HIT — returning cached cluster usage (ts=%s)", usage.Timestamp)
	return &usage
}

// setCachedClusterUsage stores ClusterUsage in Redis with TTL.
func setCachedClusterUsage(usage *ClusterUsage) {
	if redisClient == nil {
		return
	}

	data, err := json.Marshal(usage)
	if err != nil {
		log.Printf("Warning: failed to marshal cluster usage for cache: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ttl := getCacheTTL()
	if err := redisClient.Set(ctx, cacheKey, data, ttl).Err(); err != nil {
		log.Printf("Warning: failed to set cache: %v", err)
		return
	}

	log.Printf("Cache SET — stored cluster usage (TTL=%s)", ttl)
}
