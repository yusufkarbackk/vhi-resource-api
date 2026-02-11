# Total Usage API - Super Simple Dashboard

Endpoint ini **otomatis scan semua VM** di semua domain dan project, lalu return **total CPU dan RAM yang sedang dipakai saat ini**.

**Tidak perlu:**

- ❌ Specify instance IDs
- ❌ Specify time range
- ❌ Specify domain/project

**Cukup:**

- ✅ Call 1 endpoint
- ✅ Get 2 angka: CPU cores used + RAM used

---

## 🎯 API Endpoint

### **GET /api/v1/usage/total**

Mendapatkan total usage untuk **SEMUA VM** di cluster.

**No parameters needed!**

---

## 📊 Response

```json
{
  "timestamp": "2026-02-09T16:00:00Z",
  "total_vms": 45,
  "cpu_cores_used": 70.5,
  "ram_used_gb": 126.3,
  "errors": [
    {
      "domain_name": "bati-internal",
      "instance_id": "c921ed74-48e5-4fa6-b093-22d08bdda660",
      "project_id": "project-1",
      "error": "failed to get CPU measures: timeout"
    }
  ]
}
```

**Penjelasan:**

- `timestamp`: Waktu snapshot diambil
- `total_vms`: Jumlah VM yang ditemukan
- `cpu_cores_used`: **Total vCPU cores yang sedang dipakai** (70.5 cores)
- `ram_used_gb`: **Total RAM yang sedang dipakai** (126.3 GiB)
- `errors`: (opsional) daftar error jika sebagian VM/domain gagal diproses. Total tetap **parsial** sesuai PRD.

Endpoint ini bekerja dalam beberapa langkah:

1. Membaca daftar nama domain dari file `domain.txt` (satu nama per baris).
2. Login admin ke Keystone (`KEYSTONE_URL`) menggunakan `ADMIN_*` env untuk mendapatkan `X-Subject-Token`.
3. Menggunakan token admin tersebut sebagai `X-Auth-Token` ke Gnocchi (`GNOCCHI_URL`).
4. Mengambil semua VM dari Gnocchi dan hanya menghitung VM yang project-nya berada di domain-domain pada `domain.txt`.

---

## 🚀 Usage

### **Simple cURL:**

```bash
curl http://localhost:8080/api/v1/usage/total
```

**Output:**

```json
{
  "timestamp": "2026-02-09T16:00:00Z",
  "total_vms": 45,
  "cpu_cores_used": 70.5,
  "ram_used_gb": 126.3
}
```

### **With jq (pretty print):**

```bash
curl -s http://localhost:8080/api/v1/usage/total | jq
```

### **Extract only CPU and RAM:**

```bash
# CPU only
curl -s http://localhost:8080/api/v1/usage/total | jq -r '.cpu_cores_used'
# Output: 70.5

# RAM only
curl -s http://localhost:8080/api/v1/usage/total | jq -r '.ram_used_gb'
# Output: 126.3
```

---

## 💻 Dashboard HTML (Super Simple)

```html
<!DOCTYPE html>
<html>
  <head>
    <title>Resource Monitor</title>
    <style>
      body {
        font-family: Arial, sans-serif;
        display: flex;
        justify-content: center;
        align-items: center;
        height: 100vh;
        margin: 0;
        background: #f5f5f5;
      }
      .container {
        display: flex;
        gap: 40px;
      }
      .card {
        background: white;
        border: 2px solid #ddd;
        border-radius: 8px;
        padding: 40px 60px;
        text-align: center;
        box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
        min-width: 200px;
      }
      .title {
        font-size: 18px;
        color: #666;
        margin-bottom: 20px;
        font-weight: 600;
      }
      .value {
        font-size: 72px;
        font-weight: bold;
        color: #333;
        margin-bottom: 10px;
      }
      .label {
        font-size: 14px;
        color: #999;
      }
      .timestamp {
        text-align: center;
        margin-top: 30px;
        color: #666;
        font-size: 12px;
      }
    </style>
  </head>
  <body>
    <div>
      <div class="container">
        <div class="card">
          <div class="title">CPU</div>
          <div class="value" id="cpu">--</div>
          <div class="label">used</div>
        </div>

        <div class="card">
          <div class="title">RAM</div>
          <div class="value" id="ram">--</div>
          <div class="label">used</div>
        </div>
      </div>

      <div class="timestamp">Last update: <span id="timestamp">--</span></div>
    </div>

    <script>
      const API_URL = "http://localhost:8080/api/v1/usage/total";

      async function updateMetrics() {
        try {
          const response = await fetch(API_URL);
          const data = await response.json();

          // Update CPU (round to integer)
          document.getElementById("cpu").textContent = Math.round(
            data.cpu_cores_used,
          );

          // Update RAM (round to integer + add "GiB")
          document.getElementById("ram").textContent =
            Math.round(data.ram_used_gb) + " GiB";

          // Update timestamp
          const time = new Date(data.timestamp);
          document.getElementById("timestamp").textContent =
            time.toLocaleTimeString();
        } catch (error) {
          console.error("Error fetching metrics:", error);
        }
      }

      // Initial load
      updateMetrics();

      // Auto-refresh every 30 seconds
      setInterval(updateMetrics, 30000);
    </script>
  </body>
</html>
```

**Save as `dashboard.html` dan buka di browser!**

**Akan tampil:**

```
╔══════════════╗  ╔══════════════╗
║     CPU      ║  ║     RAM      ║
║              ║  ║              ║
║      70      ║  ║   126 GiB    ║
║     used     ║  ║     used     ║
╚══════════════╝  ╚══════════════╝

    Last update: 4:00:00 PM
```

---

## 🔄 Auto-Refresh Script

### **Bash Script untuk Monitoring:**

```bash
#!/bin/bash
# monitor.sh - Real-time monitoring di terminal

API_URL="http://localhost:8080/api/v1/usage/total"

while true; do
    clear
    echo "==================================="
    echo "    VHI Resource Usage Monitor"
    echo "==================================="
    echo ""

    # Fetch data
    DATA=$(curl -s $API_URL)

    # Extract values
    CPU=$(echo $DATA | jq -r '.cpu_cores_used' | awk '{printf "%.1f", $1}')
    RAM=$(echo $DATA | jq -r '.ram_used_gb' | awk '{printf "%.1f", $1}')
    VMS=$(echo $DATA | jq -r '.total_vms')
    TIME=$(echo $DATA | jq -r '.timestamp')

    # Display
    echo "  Total VMs: $VMS"
    echo ""
    echo "  CPU Cores Used: $CPU"
    echo "  RAM Used: $RAM GiB"
    echo ""
    echo "  Last Update: $TIME"
    echo ""
    echo "==================================="
    echo "  Refreshing in 30 seconds..."

    sleep 30
done
```

**Usage:**

```bash
chmod +x monitor.sh
./monitor.sh
```

---

## ⚡ Performance

### **Response Time:**

Untuk cluster dengan **100 VMs**:

- **Sequential**: ~30-60 seconds ❌
- **Parallel (10 concurrent)**: ~5-10 seconds ✅

Code sudah implement **parallel processing** dengan semaphore (max 10 concurrent requests).

### **Caching (Optional):**

Jika terlalu lambat, implement caching:

```go
var cachedResponse TotalUsage
var cacheTime time.Time
var cacheMutex sync.Mutex

func getTotalUsageWithCache(w http.ResponseWriter, r *http.Request) {
    cacheMutex.Lock()

    // Return cache if less than 30 seconds old
    if time.Since(cacheTime) < 30*time.Second {
        cacheMutex.Unlock()
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("X-Cache", "HIT")
        json.NewEncoder(w).Encode(cachedResponse)
        return
    }
    cacheMutex.Unlock()

    // Fetch fresh data
    response := fetchTotalUsage()

    // Update cache
    cacheMutex.Lock()
    cachedResponse = response
    cacheTime = time.Now()
    cacheMutex.Unlock()

    w.Header().Set("Content-Type", "application/json")
    w.Header().Set("X-Cache", "MISS")
    json.NewEncoder(w).Encode(response)
}
```

---

## 🧪 Testing

### **Test 1: Basic Call**

```bash
curl http://localhost:8080/api/v1/usage/total
```

**Expected:**

```json
{
  "timestamp": "2026-02-09T16:00:00Z",
  "total_vms": 45,
  "cpu_cores_used": 70.5,
  "ram_used_gb": 126.3
}
```

### **Test 2: Check Performance**

```bash
time curl -s http://localhost:8080/api/v1/usage/total > /dev/null
```

**Expected:** < 10 seconds untuk 100 VMs

### **Test 3: Continuous Monitoring**

```bash
watch -n 30 'curl -s http://localhost:8080/api/v1/usage/total | jq "{cpu: .cpu_cores_used, ram: .ram_used_gb}"'
```

**Output:**

```
Every 30.0s: ...

{
  "cpu": 70.5,
  "ram": 126.3
}
```

---

## 📋 Log Output

Server akan log progress saat fetching:

```
Fetching all instances from Gnocchi...
Found 45 instances
Total CPU cores used: 70.52
Total RAM used: 126.34 GB
```

---

## ⚠️ Important Notes

### **Gnocchi API Limits:**

Jika cluster punya **1000+ VMs**, consider:

1. **Increase semaphore** dari 10 ke 50:

   ```go
   semaphore := make(chan struct{}, 50)
   ```

2. **Add timeout**:

   ```go
   ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
   defer cancel()
   ```

3. **Implement caching** (lihat contoh di atas)

4. **Filter by project** jika tidak perlu semua:
   ```go
   instances := filterByProject(allInstances, "project-id")
   ```

---

## 🎯 Use Cases

### **1. Dashboard Display**

```
Display simple metrics untuk NOC/monitoring team
Update setiap 30 detik
```

### **2. Alerting**

```bash
#!/bin/bash
CPU=$(curl -s http://localhost:8080/api/v1/usage/total | jq -r '.cpu_cores_used')

if (( $(echo "$CPU > 150" | bc -l) )); then
    echo "ALERT: High CPU usage: $CPU cores"
    # Send notification
fi
```

### **3. Capacity Planning**

```bash
# Log usage setiap jam untuk trend analysis
while true; do
    curl -s http://localhost:8080/api/v1/usage/total | \
        jq '{time: .timestamp, cpu: .cpu_cores_used, ram: .ram_used_gb}' \
        >> usage_log.json
    sleep 3600
done
```

---

## 📊 Summary

**Super Simple!**

1. **Call:** `GET /api/v1/usage/total`
2. **Get:**
   - `cpu_cores_used`: 70.5
   - `ram_used_gb`: 126.3
3. **Display:** Dashboard HTML
4. **Refresh:** Every 30 seconds

**No configuration needed - works out of the box!** 🎉

---

## 🔧 Troubleshooting

### **Problem: Too Slow (>30 seconds)**

**Solution:**

1. Increase semaphore: `semaphore := make(chan struct{}, 20)`
2. Enable caching (30 second TTL)
3. Check Gnocchi performance

### **Problem: Some VMs missing**

**Solution:**

- Check Gnocchi has all instances: `curl -H "X-Auth-Token: $TOKEN" https://vhi:8041/v1/resource/instance`
- Verify VM has metrics: check `metrics` field not empty

### **Problem: Zero CPU/RAM**

**Solution:**

- VMs might be idle (valid!)
- Or no recent data (last 5 minutes)
- Check individual VM: `GET /api/v1/current/{instance_id}`

---

**Persis yang Anda mau - simple & clean!** ✅
