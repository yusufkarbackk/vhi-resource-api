# VHI Billing API

API Server untuk mendapatkan usage resource CPU dan Memory dari Virtuozzo Hybrid Infrastructure (VHI) melalui Gnocchi untuk keperluan billing/penagihan customer.

## Fitur

- ✅ Mendapatkan CPU usage dalam periode waktu tertentu (default: 1 bulan terakhir)
- ✅ Mendapatkan Memory usage dengan statistik lengkap
- ✅ Kalkulasi billing otomatis berdasarkan penggunaan
- ✅ Support custom pricing per resource
- ✅ Laporan harian dan per jam
- ✅ Statistik: Average, Max, Min, Median, Percentile 95

## Prerequisites

- Go 1.21 atau lebih tinggi
- Akses ke VHI Gnocchi API
- Authentication token yang valid

## Instalasi

```bash
# Clone atau copy semua file ke direktori project
cd vhi-billing-api

# Download dependencies
go mod download

# Build aplikasi
go build -o billing-api
```

## Konfigurasi

Set environment variables dasar:

```bash
export GNOCCHI_URL="https://your-vhi-endpoint:8041/v1"
export KEYSTONE_URL="https://your-keystone-endpoint:5000/v3"

# Kredensial admin (untuk login ke Keystone dan mendapatkan X-Subject-Token)
export ADMIN_USERNAME="admin-user"
export ADMIN_PASSWORD="admin-password"
export ADMIN_DOMAIN_ID="default-domain-id"          # domain.id untuk user admin
export ADMIN_PROJECT_NAME="admin-project-name"      # nama project scope admin
export ADMIN_PROJECT_DOMAIN_ID="default-domain-id"  # domain.id untuk project admin
```

Untuk endpoint total usage (`/api/v1/usage/total`), token ke Gnocchi **tidak lagi dibaca dari `.env`**,
melainkan selalu diambil dari Keystone dengan login admin (X-Subject-Token → X-Auth-Token).

## Menjalankan Server

```bash
# Development
go run .

# Production (setelah build)
./billing-api
```

Server akan berjalan di port `8080`.

## API Endpoints

### 1. Health Check

```bash
GET /health
```

**Response:**
```json
{
  "status": "healthy",
  "time": "2026-02-08T10:30:00Z"
}
```

---

### 2. Total Usage Snapshot (Cluster-wide)

Mendapatkan **snapshot total usage CPU & RAM** untuk semua VM di semua domain/project.

```bash
GET /api/v1/usage/total
```

**Response:**

```json
{
  "timestamp": "2026-02-09T16:00:00Z",
  "total_vms": 45,
  "cpu_cores_used": 70.5,
  "ram_used_gb": 126.3,
  "errors": [
    {
      "instance_id": "c921ed74-48e5-4fa6-b093-22d08bdda660",
      "project_id": "project-1",
      "error": "failed to get CPU measures: timeout"
    }
  ]
}
```

**Catatan:**

- Jika sebagian VM/domain gagal diproses, API **tetap mengembalikan total parsial** dan mengisi field `errors` dengan daftar kegagalan tersebut.
- Endpoint ini menggunakan **admin token dari Keystone** (bukan lagi `GNOCCHI_TOKEN` di `.env`), kemudian:
  - Membaca daftar nama domain dari `domain.txt` (satu nama per baris).
  - Melakukan login admin sekali → mendapatkan `X-Subject-Token`.
  - Menggunakan token tersebut sebagai `X-Auth-Token` untuk call Gnocchi.
  - Mengambil semua VM dari Gnocchi dan hanya menghitung VM yang berada di project-project milik domain-domain di `domain.txt`.

---

### 3. Get CPU Billing

Mendapatkan CPU usage dan billing information untuk 1 bulan.

```bash
GET /api/v1/billing/cpu/{instance_id}
```

**Query Parameters (Optional):**
- `start_date` - Start date (format: `2006-01-02T15:04:05`)
- `end_date` - End date (format: `2006-01-02T15:04:05`)

**Example:**

```bash
# Default (last month)
curl -X GET http://localhost:8080/api/v1/billing/cpu/c921ed74-48e5-4fa6-b093-22d08bdda660

# Custom date range
curl -X GET "http://localhost:8080/api/v1/billing/cpu/c921ed74-48e5-4fa6-b093-22d08bdda660?start_date=2026-01-01T00:00:00&end_date=2026-01-31T23:59:59"
```

**Response:**
```json
{
  "instance_id": "c921ed74-48e5-4fa6-b093-22d08bdda660",
  "instance_name": "vm-ucups-5",
  "start_date": "2026-01-01T00:00:00",
  "end_date": "2026-01-31T23:59:59",
  "vcpus": 2,
  "usage": {
    "total_data_points": 744,
    "average_percent": 0.28,
    "max_percent": 1.45,
    "min_percent": 0.12,
    "median_percent": 0.26,
    "percentile_95": 0.42,
    "usage_by_hour": [...],
    "usage_by_day": [
      {
        "date": "2026-01-01",
        "average_cpu_percent": 0.27,
        "max_cpu_percent": 0.89,
        "min_cpu_percent": 0.15,
        "total_cpu_hours": 0.13
      }
    ]
  },
  "billing": {
    "total_cpu_hours": 4.2,
    "total_cpu_core_hours": 4.2,
    "average_cpu_percent": 0.28,
    "billing_period_days": 31,
    "billing_period_hours": 744.0
  }
}
```

---

### 4. Get All Resource Billing

Mendapatkan CPU dan Memory usage.

```bash
GET /api/v1/billing/resources/{instance_id}
```

**Query Parameters (Optional):**
- `start_date` - Start date
- `end_date` - End date

**Example:**

```bash
curl -X GET http://localhost:8080/api/v1/billing/resources/c921ed74-48e5-4fa6-b093-22d08bdda660
```

**Response:**
```json
{
  "instance_id": "c921ed74-48e5-4fa6-b093-22d08bdda660",
  "instance_name": "vm-ucups-5",
  "flavor_name": "medium",
  "start_date": "2026-01-01T00:00:00",
  "end_date": "2026-01-31T23:59:59",
  "vcpus": 2,
  "cpu": {
    "total_data_points": 744,
    "average_percent": 0.28,
    "max_percent": 1.45,
    "min_percent": 0.12,
    "usage_by_day": [...]
  },
  "memory": {
    "average_used_mb": 3200.5,
    "average_used_gb": 3.13,
    "max_used_mb": 3850.2,
    "min_used_mb": 2100.8,
    "average_percent": 76.2,
    "total_memory_mb": 4194304,
    "usage_by_day": [...]
  }
}
```

---

### 5. Get Billing Report (dengan pricing)

Mendapatkan laporan billing lengkap dengan kalkulasi biaya.

```bash
GET /api/v1/billing/report/{instance_id}
```

**Query Parameters (Optional):**
- `start_date` - Start date
- `end_date` - End date
- `cpu_price_per_hour` - Price per CPU core hour (default: 0.05)
- `memory_price_per_gb` - Price per GB hour (default: 0.01)

**Example:**

```bash
# Default pricing
curl -X GET http://localhost:8080/api/v1/billing/report/c921ed74-48e5-4fa6-b093-22d08bdda660

# Custom pricing
curl -X GET "http://localhost:8080/api/v1/billing/report/c921ed74-48e5-4fa6-b093-22d08bdda660?cpu_price_per_hour=0.08&memory_price_per_gb=0.015"
```

**Response:**
```json
{
  "instance_id": "c921ed74-48e5-4fa6-b093-22d08bdda660",
  "instance_name": "vm-ucups-5",
  "flavor_name": "medium",
  "start_date": "2026-01-01T00:00:00",
  "end_date": "2026-01-31T23:59:59",
  "generated_at": "2026-02-08T10:30:00Z",
  "currency": "USD",
  "vcpus": 2,
  "cpu_usage": {
    "average_percent": 0.28,
    "total_data_points": 744
  },
  "memory_usage": {
    "average_used_gb": 3.13,
    "average_percent": 76.2
  },
  "cpu_price_per_hour": 0.05,
  "memory_price_per_gb_hour": 0.01,
  "cpu_cost": 0.21,
  "memory_cost": 23.29,
  "total_cost": 23.50
}
```

---

## Contoh Integrasi

### Python

```python
import requests
import json

base_url = "http://localhost:8080/api/v1"
instance_id = "c921ed74-48e5-4fa6-b093-22d08bdda660"

# Get billing report
response = requests.get(
    f"{base_url}/billing/report/{instance_id}",
    params={
        "start_date": "2026-01-01T00:00:00",
        "end_date": "2026-01-31T23:59:59",
        "cpu_price_per_hour": 0.10,
        "memory_price_per_gb": 0.02
    }
)

report = response.json()
print(f"Instance: {report['instance_name']}")
print(f"Total Cost: ${report['total_cost']:.2f}")
print(f"  - CPU Cost: ${report['cpu_cost']:.2f}")
print(f"  - Memory Cost: ${report['memory_cost']:.2f}")
```

### cURL

```bash
#!/bin/bash

INSTANCE_ID="c921ed74-48e5-4fa6-b093-22d08bdda660"
START_DATE="2026-01-01T00:00:00"
END_DATE="2026-01-31T23:59:59"

# Get billing report
curl -X GET \
  "http://localhost:8080/api/v1/billing/report/${INSTANCE_ID}?start_date=${START_DATE}&end_date=${END_DATE}&cpu_price_per_hour=0.08&memory_price_per_gb=0.015" \
  | jq '.'
```

### JavaScript/Node.js

```javascript
const axios = require('axios');

const baseURL = 'http://localhost:8080/api/v1';
const instanceId = 'c921ed74-48e5-4fa6-b093-22d08bdda660';

async function getBillingReport() {
  try {
    const response = await axios.get(
      `${baseURL}/billing/report/${instanceId}`,
      {
        params: {
          start_date: '2026-01-01T00:00:00',
          end_date: '2026-01-31T23:59:59',
          cpu_price_per_hour: 0.10,
          memory_price_per_gb: 0.02
        }
      }
    );
    
    const report = response.data;
    console.log(`Instance: ${report.instance_name}`);
    console.log(`Total Cost: $${report.total_cost.toFixed(2)}`);
    console.log(`  - CPU: $${report.cpu_cost.toFixed(2)}`);
    console.log(`  - Memory: $${report.memory_cost.toFixed(2)}`);
  } catch (error) {
    console.error('Error:', error.message);
  }
}

getBillingReport();
```

## Penjelasan Kalkulasi

### CPU Billing

1. **CPU Time**: Diambil dari Gnocchi metric `cpu` (cumulative nanoseconds)
2. **Delta Calculation**: CPU time antar periode dihitung untuk mendapat usage
3. **Percentage**: `(delta_cpu_time / (granularity * vcpus)) * 100`
4. **CPU Hours**: Total CPU seconds dikonversi ke hours
5. **Cost**: `total_cpu_hours * price_per_hour`

### Memory Billing

1. **Memory Usage**: Diambil dari metric `memory.usage` (MB)
2. **Average**: Rata-rata usage selama periode
3. **GB-Hours**: `(average_memory_gb * total_hours)`
4. **Cost**: `gb_hours * price_per_gb_hour`

## Desain Lanjutan: Domain List & Auth Per-Domain (Next Iteration)

Untuk menyelaraskan penuh dengan PRD, iterasi berikutnya dapat menambahkan:

- **File `domains.txt`**:
  - Format contoh per baris: `domain_name;project_id;username;password`
  - Dibaca saat startup untuk menentukan domain/project mana saja yang akan dihitung.
- **Login Keystone per-domain**:
  - Gunakan kredensial dari `domains.txt` untuk call ke Keystone.
  - Ambil `X-Subject-Token` dari response header dan gunakan sebagai `X-Auth-Token` saat call ke Gnocchi.
- **GnocchiClient dengan token dinamis**:
  - Modifikasi agar token tidak hanya dari env, tapi bisa di-inject per permintaan (misal melalui parameter atau context).
- **Flow `getTotalUsage` per-domain**:
  - Loop domain dalam `domains.txt`, login domain admin (sekali per domain), lalu ambil instance untuk project terkait.
  - Proses usage tetap paralel, namun sudah ter-segregasi per domain sesuai kebutuhan billing multi-domain.

## Tips Deployment

### Systemd Service

Create `/etc/systemd/system/billing-api.service`:

```ini
[Unit]
Description=VHI Billing API Server
After=network.target

[Service]
Type=simple
User=billing-api
WorkingDirectory=/opt/billing-api
Environment="GNOCCHI_URL=https://your-vhi:8041/v1"
Environment="GNOCCHI_TOKEN=your-token"
ExecStart=/opt/billing-api/billing-api
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable billing-api
sudo systemctl start billing-api
```

### Docker

Create `Dockerfile`:

```dockerfile
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o billing-api

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/billing-api .

EXPOSE 8080
CMD ["./billing-api"]
```

Build and run:

```bash
docker build -t vhi-billing-api .
docker run -d -p 8080:8080 \
  -e GNOCCHI_URL="https://your-vhi:8041/v1" \
  -e GNOCCHI_TOKEN="your-token" \
  vhi-billing-api
```

### Nginx Reverse Proxy

```nginx
server {
    listen 80;
    server_name billing-api.yourdomain.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

## Troubleshooting

### Issue: "Failed to get instance"

**Solusi:**
- Pastikan GNOCCHI_TOKEN valid
- Cek instance_id benar
- Verifikasi koneksi ke Gnocchi API

### Issue: "API returned status 401"

**Solusi:**
- Token expired, generate token baru dari Keystone
- Pastikan token memiliki akses ke project yang sesuai

### Issue: "No CPU metrics found"

**Solusi:**
- VM mungkin baru dibuat, tunggu beberapa menit
- Cek apakah Ceilometer/Gnocchi collecting metrics
- Verifikasi archive policy di Gnocchi

## License

MIT License - feel free to use for commercial purposes.

## Support

Untuk pertanyaan atau issue, silakan buat issue di repository.
