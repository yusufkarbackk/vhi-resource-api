 ### VHI BILLING API ###

 ### requirements
 - langsung memberi hasil total usage resouce CPU dan RAM vm dari semua domain dan project
  
 - hanya ada 2 nilai yang dihasilkan yaitu total usage CPU dan RAM yang di ambil saat itu juga dan tidak ada rentang waktu historis

 - untuk hit endpoint yang mendapatkan resource harus autentikasi domain admin dan mendapatkan token dari response header dengan nama X-Subcject-Token, karena ada banyak domain dan banyak project dari masing-masing domain coba untuk menggunakan goroutine agar tidak saling menunggu seperti menggunakan looping

 - 1 proses login domain admin, mendapatkan token dari header, gunakan token tersebut di header request dengan nama X-Auth-Token untuk mendapatkan resource

 - hasil total resource CPU dan RAM dari masing-masing domain yang di dapat dari setiap proses akan di tambahkan dan menghasilkan total resource dari semua domain

- list domain yang akan di cek akan di simpan dalam bentuk file txt agar mudah untuk update atau delete


- total adalah jumlah vcpu dan ram yang terpakai saat ini dari seluruh domain

-handling errorper jika ada domain gagal adalah tetap kembalikan total parsial + dafar error

- batas maximal timeout tes 5 menit dulu

### dokumentasi

- https://docs.openstack.org/2025.2/api/
- https://docs.virtuozzo.com/virtuozzo_hybrid_infrastructure_7_2_compute_api_reference/index.html
- https://gnocchi.osci.io/rest.html
- https://docs.openstack.org/newton/cli-reference/gnocchi.html
- cinder: https://docs.openstack.org/api-ref/block-storage/v3/
- provisioned storage space: https://docs.virtuozzo.com/virtuozzo_infrastructure_7_2_compute_api_reference/index.html#aggregating-provisioned-storage-space.html

### autentikasi

- untuk endpoint request token admin adalah :5000/v3/auth/tokens

- request body untuk endpoint request token
{
    "auth": {
        "identity": {
            "methods": [
                "password"
            ],
            "password": {
                "user": {
                    "name": {username},
                    "domain": {
                        "id": {domain_id}
                    },
                    "password": {password}
                }
            }
        },
        "scope": {
            "project": {
                "name": {admin domain name},
                "domain": {
                    "id": {admin domain_id}
                }
            }
        }
    }
}
