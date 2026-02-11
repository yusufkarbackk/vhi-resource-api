# Quick Start Guide - VHI Billing API

## Setup Cepat (5 Menit)

### 1. Persiapan Environment

```bash
# Copy environment template
cp .env.example .env

# Edit .env dengan credentials Anda
nano .env
```

Isi `.env`:
```
GNOCCHI_URL=https://10.10.10.100:8041/v1
GNOCCHI_TOKEN=gAAAAABl...your-token...
```

### 2. Cara Mendapatkan Token

```bash
# Login ke Keystone untuk mendapatkan token
curl -X POST https://your-vhi:5000/v3/auth/tokens \
  -H "Content-Type: application/json" \
  -d '{
    "auth": {
      "identity": {
        "methods": ["password"],
        "password": {
          "user": {
            "name": "admin",
            "domain": {"name": "Default"},
            "password": "your-password"
          }
        }
      },
      "scope": {
        "project": {
          "name": "admin",
          "domain": {"name": "Default"}
        }
      }
    }
  }' -i | grep X-Subject-Token
```

Copy token dari response header `X-Subject-Token`.

### 3. Install & Run

**Opsi A: Menggunakan Go (Development)**

```bash
# Install dependencies
go mod download

# Run server
go run .
```

**Opsi B: Build Binary (Production)**

```bash
# Build
go build -o billing-api

# Run
./billing-api
```

**Opsi C: Menggunakan Docker**

```bash
# Build dan run dengan docker-compose
docker-compose up -d

# Cek logs
docker-compose logs -f
```

### 4. Test API

```bash
# Health check
curl http://localhost:8080/health

# Test billing untuk instance tertentu
curl "http://localhost:8080/api/v1/billing/report/YOUR-INSTANCE-ID" | jq '.'
```

Atau gunakan test script:

```bash
# Edit INSTANCE_ID di test.sh terlebih dahulu
nano test.sh

# Run tests
./test.sh
```

## Contoh Response

### CPU Billing Response

```json
{
  "instance_id": "c921ed74-48e5-4fa6-b093-22d08bdda660",
  "instance_name": "vm-ucups-5",
  "start_date": "2026-01-01T00:00:00",
  "end_date": "2026-01-31T23:59:59",
  "vcpus": 2,
  "usage": {
    "average_percent": 0.28,
    "max_percent": 1.45,
    "min_percent": 0.12,
    "median_percent": 0.26,
    "percentile_95": 0.42,
    "usage_by_day": [...]
  },
  "billing": {
    "total_cpu_hours": 4.2,
    "average_cpu_percent": 0.28,
    "billing_period_days": 31
  }
}
```

### Billing Report (dengan Cost)

```json
{
  "instance_name": "vm-ucups-5",
  "flavor_name": "medium",
  "start_date": "2026-01-01T00:00:00",
  "end_date": "2026-01-31T23:59:59",
  "cpu_cost": 0.21,
  "memory_cost": 23.29,
  "total_cost": 23.50,
  "currency": "USD"
}
```

## Customize Pricing

Anda bisa set custom pricing per request:

```bash
curl "http://localhost:8080/api/v1/billing/report/YOUR-INSTANCE-ID?\
cpu_price_per_hour=0.10&\
memory_price_per_gb=0.02"
```

Atau set default di environment:

```bash
export DEFAULT_CPU_PRICE_PER_HOUR=0.10
export DEFAULT_MEMORY_PRICE_PER_GB=0.02
```

## Troubleshooting Cepat

**Error: Connection refused**
- Pastikan Gnocchi URL benar
- Cek firewall/network access ke VHI

**Error: 401 Unauthorized**
- Token expired, generate token baru
- Pastikan token punya akses ke project

**Error: Metric not found**
- VM mungkin baru, tunggu beberapa menit
- Cek Ceilometer/Gnocchi sedang collecting

**Low CPU usage (< 1%)**
- Normal untuk VM idle
- Cek workload di VM apakah memang ringan

## Next Steps

1. ✅ Integrasikan dengan sistem billing Anda
2. ✅ Setup monitoring untuk API ini
3. ✅ Tambahkan authentication jika diperlukan
4. ✅ Setup automated reports (cron job)
5. ✅ Deploy ke production dengan reverse proxy

## Support

Untuk dokumentasi lengkap, lihat `README.md`.
