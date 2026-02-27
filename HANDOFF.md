# VHI Billing API — Handoff Document

## Tujuan Akhir

Menambahkan field **vstorage logical storage** ke response endpoint `/api/v1/cluster/usage`:
```json
{
  "logical_storage_total_tib": 323.8,
  "logical_storage_used_tib": 75.1,
  "logical_storage_free_tib": 248.5
}
```
Data ini seharusnya match dengan grafik Grafana di VHI panel (vStorage Total / Usage / Free).

---

## Apa yang Sudah Selesai ✅

### 1. VHI Panel Client Singleton
- File: `vhi_panel.go`
- Login dilakukan sekali saat startup, token di-cache (singleton `panelClient`)
- Auto re-login saat token expire (401) via `doAuthGet()` helper

### 2. Compute Cluster Stat (Bekerja ✅)
- Endpoint: `GET /api/v2/compute/cluster/stat`
- Data: vCPUs, RAM, Block Storage (9.54 TiB) — cocok dengan VHI dashboard
- Struct: `PanelStat` di `vhi_panel.go`
- Field di response: `provisioned_storage_tib`, `storage_used_tib`, `storage_free_tib`

### 3. Logical Storage Fields (Ditambahkan, tapi masih 0)
- File: `clusterUsage.go` — tambah field `LogicalStorageTotalTiB`, `LogicalStorageUsedTiB`, `LogicalStorageFreeTiB`
- File: `vhi_panel.go` — tambah `VStorageStat` struct + `GetStorageStat()` method

---

## Masalah Saat Ini ❌

### Target Data
Data yang dicari: output dari `vstorage -c vhipdg01 stat` (CLI):
```
Space: allocatable 287TB of 377TB, free 314TB of 386TB
```
Atau dari Grafana dashboard (Tier 0 + Tier 1 combined):
- Tier 0: Total ~262 TiB, Usage ~68.7 TiB
- Tier 1: Total ~61.9 TiB, Usage ~6.42 TiB

### Grafana Prometheus Queries (Sumber Data yang Benar)
Grafana menggunakan Prometheus datasource dengan query:
```
sum(tier:mdsd_fs_space_bytes:sum{cloud=""})       → Total
sum(tier:mdsd_fs_free_space_bytes:sum{cloud=""})  → Free
```

### Endpoint yang Dicoba (Semua GAGAL)

| Endpoint | Status | Keterangan |
|---|---|---|
| `/api/v2/storage/cluster/stat` | 404 | Tidak exist di VHI panel ini |
| `/api/v2/storage/cluster/vhipdg01/stat` | 404 | Tidak exist |
| `/api/v2/storage/stat` | 404 | Tidak exist |
| `/grafana/api/datasources/proxy/1/api/v1/query` | 401 | Grafana session needed |
| `/grafana/api/datasources/1/resources/api/v1/query` | 401 | Grafana session needed |
| `/grafana/api/login` (POST) | 401 | Tidak menerima credentials |
| `/login` (form POST) | 405 | Method Not Allowed |
| `/auth/login` (form POST) | 405 | Method Not Allowed |
| `/webcp/login` (form POST) | 405 | Method Not Allowed |

### Root Cause
VHI panel API login (`/api/v2/login`) menghasilkan cookie `session` (bukan `session0`).
Browser mendapatkan `session0=UUID.Signature` + `grafana_session=...` melalui SSO flow.
Kita belum bisa reproduce SSO flow ini secara programmatik.

---

## Solusi yang Belum Dicoba (Next Steps)

### Opsi 1: Direct Prometheus (Paling Mudah)
Prometheus di VHI panel berjalan di `http://127.0.0.1:9090` (internal).
Test apakah accessible dari production server atau dev:
```bash
curl http://10.21.0.240:9090/api/v1/query?query=up
curl http://cloudpanel.mybaticloud.com:9090/api/v1/query?query=up
```
Jika port 9090 terbuka, implementasikan `PROMETHEUS_URL=http://10.21.0.240:9090` di `.env`.

### Opsi 2: Grafana API Key
Di Grafana dashboard:
1. Login ke `https://cloudpanel.mybaticloud.com:8888/grafana`
2. Buka **Configuration → API Keys**
3. Buat API key baru
4. Gunakan `Authorization: Bearer <api_key>` header

### Opsi 3: SSH ke VHI Node
Jika production server bisa SSH ke VHI storage node:
```bash
ssh root@<storage-node> "vstorage -c vhipdg01 stat"
```
Parse output dan extract allocatable total/used/free.

### Opsi 4: Grafana Service Account (Grafana 8+)
Di Grafana, buat Service Account dengan role Viewer.
Gunakan Service Account token untuk API calls.

---

## Environment Variables

```env
VHI_PANEL_URL=https://cloudpanel.mybaticloud.com:8888
ADMIN_USERNAME=admin
ADMIN_PASSWORD=kiclikKINI#131Z!1
```

## File-File Penting

| File | Fungsi |
|---|---|
| `vhi_panel.go` | VHI Panel client, Login(), GetStat(), GetStorageStat() |
| `clusterUsage.go` | Handler `/api/v1/cluster/usage`, struct `ClusterUsage` |
| `main.go` | Singleton `panelClient` initialization |
| `.env` | Environment variables |

## Current Code State

`GetStorageStat()` di `vhi_panel.go`:
- Mencoba login Grafana via web UI form (gagal 405)
- Kemudian mencoba Prometheus query via Grafana proxy dengan cookie (gagal 401)
- Response: `logical_storage_*` fields semua 0

---

## Cara Deploy ke Production

```bash
# Di production server (10.23.6.103)
cd /opt/vhi-resource-api
git pull origin main
/usr/local/go/bin/go build -o vhi-resource-api .
sudo systemctl restart vhi-resource-api
sudo journalctl -u vhi-resource-api -f
```
