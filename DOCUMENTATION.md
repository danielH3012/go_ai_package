# Dokumentasi `go_ai_package`

Library AI Agent Go modular untuk membangun chatbot berbasis MCP (Model Context Protocol) dengan dukungan multi-turn session, RBAC, dan ekstraksi dokumen.

---

## Daftar Isi

1. [Konfigurasi Global](#1-konfigurasi-global)
2. [Tiga Agen Utama AI](#2-tiga-agen-utama-ai)
3. [Koneksi MCP, Tool Execution & Identifier Engine](#3-koneksi-mcp--tool-execution)
4. [RBAC (Role-Based Access Control)](#4-rbac-role-based-access-control)
5. [Database Riwayat Chat](#5-database-riwayat-chat)
6. [Ekstraksi Dokumen & OCR](#6-ekstraksi-dokumen--ocr)
7. [Tipe Data & Functional Options](#7-tipe-data--functional-options)
8. [Contoh Alur Lengkap](#8-contoh-alur-lengkap)

---

## 1. Konfigurasi Global

### `SetAIConfig(apiKey, baseURL string)`

Mengatur kredensial LLM secara terprogram. Panggil sekali di awal aplikasi.

| Parameter | Tipe | Keterangan |
|---|---|---|
| `apiKey` | `string` | API key penyedia LLM |
| `baseURL` | `string` | Base URL endpoint LLM |

```go
// OpenRouter
goaipackage.SetAIConfig("sk-or-v1-xxx", "https://openrouter.ai/api/v1")

// OpenAI langsung
goaipackage.SetAIConfig("sk-xxx", "https://api.openai.com/v1")

// DeepSeek langsung
goaipackage.SetAIConfig("sk-xxx", "https://api.deepseek.com")

// Hanya mengatur salah satu (kirim string kosong "" untuk nilai yang tidak ingin diubah)
goaipackage.SetAIConfig("sk-xxx", "")
goaipackage.SetAIConfig("", "https://openrouter.ai/api/v1")
```

> [!TIP]
> Jika tidak memanggil `SetAIConfig`, library akan membaca dari environment variable `AI_KEY` / `OPENAI_API_KEY` dan `BASE_AI_URL`, atau file `.env` di direktori kerja.

---

### `DefaultModel` & `GeneratorModel`

Model default yang digunakan jika parameter `model` di-pass `""` (string kosong).

```go
const DefaultModel = "liquid/lfm-2.5-2.6b:free"
var GeneratorModel = DefaultModel
```

Untuk override model default secara global:
```go
goaipackage.GeneratorModel = "openai/gpt-4o-mini"
```

---

### `ResolveModel(name string) string`

Mengonversi alias model ke nama lengkap. Jika nama tidak ada di katalog alias, dikembalikan as-is.

| Input | Output |
|---|---|
| `""` | `"liquid/lfm-2.5-2.6b:free"` (default) |
| `"lfm"` | `"liquid/lfm-2.5-2.6b:free"` |
| `"lfm-2.5"` | `"liquid/lfm-2.5-2.6b:free"` |
| `"openai/gpt-4o"` | `"openai/gpt-4o"` (fallback as-is) |

```go
model := goaipackage.ResolveModel("lfm") // → "liquid/lfm-2.5-2.6b:free"
```

---

### `SanitizeModelReply(raw string) string`

Membersihkan output mentah LLM dari tag internal yang tidak boleh muncul ke user: `<thought>`, `<think>`, token DSML, `<tool_call>`, `<function_calls>`, sisa JSON payload, dll.

```go
clean := goaipackage.SanitizeModelReply(result.RawOutput)
```

---

## 2. Tiga Agen Utama AI

Ketiga agen ini adalah API utama library. Semua mengembalikan **JSON string** agar mudah diteruskan ke REST API / frontend.

### Mekanisme `userContext`

Parameter `userContext map[string]any` adalah "tas konteks sesi" yang diisi oleh `repo.LoadContext()`. Berisi:

| Key | Alternatif / Alias | Isi |
|---|---|---|
| `"auth"` | `UserAuth` struct / map | Objek auth lengkap (Role, UserID, CompanyID, Name) |
| `"role"` | `"Role"` | `string` — role pengguna untuk filter RBAC |
| `"user_id"` | `"UserID"`, `"userId"` | `string` — ID pengguna |
| `"company"` | `"company_id"`, `"CompanyID"` | `string` — nama/ID perusahaan |
| `"username"` | `"name"`, `"Name"` | `string` — nama pengguna |
| `"history"` | — | `[]ChatMessage` — 10 pesan terakhir sesi |
| `"attachment_text"` | — | `string` — teks dokumen yang dilampirkan |
| `"attachment_name"` | — | `string` — nama file lampiran |
| `"has_user_uploaded_file"` | — | `bool` — apakah ada upload baru |

> [!TIP]
> **Bebas Redundansi**: Anda cukup mengeset objek `userContext["auth"] = req.Auth` atau cukup satu set key standar (`role`, `user_id`, `company`, `name`). Library menangani alias dan resolusi otomatis di belakang layar — tidak perlu lagi menginjeksi alias berulang kali!

Ketiga agen **otomatis membaca** `userContext` ini untuk menyuntikkan riwayat chat, lampiran, dan info company ke dalam prompt LLM.

---

### Agen 1: `SearchIntent`

Mengklasifikasi intent user: tool mana yang dibutuhkan, apakah pertanyaan relevan atau off-topic, dan apakah operasi bersifat mutasi data.

```go
func SearchIntent(
    ctx         context.Context,
    query       string,
    userContext  map[string]any,
    model       string,
    intentOpts  ...RequestIntentOption,
) (string, error)
```

**Return** — JSON string:
```json
{
  "tools":       ["nama_tool"],
  "reason":      "alasan pemilihan tool",
  "target_tool": "nama_tool",
  "category":    "nama_tool",
  "is_mutation":  false,
  "is_delete":    false,
  "is_update":    false,
  "is_report":    false,
  "is_off_topic": false
}
```

**Contoh**:
```go
intentJSON, err := goaipackage.SearchIntent(ctx, "Tampilkan daftar aset", userCtx, "")
// → {"tools":["list_assets"],"target_tool":"list_assets","is_off_topic":false,...}
```

**Custom prompt** (untuk batasi scope tool):
```go
intentJSON, err := goaipackage.SearchIntent(
    ctx, query, userCtx, "",
    goaipackage.WithIntentPrompt("Hanya gunakan tool yang berhubungan dengan aset perusahaan."),
)
```

---

### Agen 2: `CallTools`

Menjalankan reasoning loop multi-turn: pilih tool → eksekusi tool (paralel untuk Read, sekuensial aman untuk Write) → ekstrak fakta → ulangi jika data belum cukup. Menegakkan RBAC dan resolusi ID secara otomatis.

```go
func CallTools(
    ctx           context.Context,
    client        *client.Client,
    mcpTools      []mcp.Tool,
    query         string,
    userContext   map[string]any,
    maxIterations int,
    model         string,
    toolsOpts     ...RequestToolsOption,
) (string, error)
```

| Parameter | Keterangan |
|---|---|
| `client` | Koneksi MCP server dari `ConnectAndLoadKnownTools` |
| `mcpTools` | Daftar tool dari MCP server |
| `query` | Pertanyaan atau perintah dari user |
| `userContext` | Tas konteks sesi (RBAC `role` dibaca otomatis dari `userContext["role"]` atau `userContext["auth"]`) |
| `maxIterations` | Batas iterasi loop (minimal 3, lebih besar = lebih teliti) |
| `model` | Nama/alias model (kosong `""` = pakai `GeneratorModel`) |
| `toolsOpts` | Opsi tambahan (mis. `WithToolsPrompt(...)`) |

**Return** — JSON string:
```json
{
  "context":      "fakta-fakta yang diekstrak dari hasil tool",
  "attachment":   {"name":"...", "url":"...", "type":"...", "size": 0},
  "tool_results": [{"tool":"...", "args":{}, "result":{}}]
}
```

> [!NOTE]
> Field `"attachment"` hanya muncul jika tool menghasilkan file (PDF/CSV/Excel).
> Field `"context"` inilah yang diteruskan ke `GenerateResponse` sebagai `contextStr`.

**Contoh**:
```go
toolJSON, err := goaipackage.CallTools(
    ctx, mcpClient, mcpTools,
    "Tampilkan 5 aset termahal",
    userCtx, 3, "",
)
```

**Custom guardrail**:
```go
toolJSON, err := goaipackage.CallTools(
    ctx, mcpClient, mcpTools, query, userCtx, 3, "",
    goaipackage.WithToolsPrompt("Jangan pernah memanggil tool delete tanpa konfirmasi eksplisit dari user."),
)
```

---

### Agen 3: `GenerateResponse`

Menyusun jawaban akhir berbahasa manusiawi berdasarkan data konteks hasil tool dan riwayat percakapan.

```go
func GenerateResponse(
    ctx        context.Context,
    query      string,
    contextStr string,
    userContext map[string]any,
    model      string,
    respOpts   ...RequestResponseOption,
) (string, error)
```

| Parameter | Keterangan |
|---|---|
| `contextStr` | String hasil `CallTools` (field `"context"`) atau data lain |
| `respOpts` | Prompt kustom untuk mengubah persona/gaya jawaban AI |

**Return** — JSON string:
```json
{ "answer": "Berikut adalah 5 aset termahal: ..." }
```

**Contoh**:
```go
respJSON, err := goaipackage.GenerateResponse(
    ctx,
    "Tampilkan 5 aset termahal",
    toolJSON,   // langsung dari CallTools
    userCtx,
    "",
    goaipackage.WithResponsePrompt("Kamu adalah Ara, asisten aset perusahaan. Jawab singkat dan profesional."),
)

var out struct{ Answer string `json:"answer"` }
json.Unmarshal([]byte(respJSON), &out)
fmt.Println(out.Answer)
```

---

## 3. Koneksi MCP & Tool Execution

### `ConnectAndLoadKnownTools(ctx, mcpLink) (*client.Client, []mcp.Tool, error)`

Cara tercepat untuk terkoneksi ke MCP server dan mendapatkan daftar tool sekaligus.

Mendukung dua jenis koneksi:
- **HTTP/SSE** — URL `http://` atau `https://`
- **Stdio** — path ke file Go/executable lokal

```go
// Via SSE (HTTP)
link := goaipackage.NewMCPLink(goaipackage.WithMCPLink("http://localhost:3000/sse"))

// Via Stdio (lokal)
link := goaipackage.NewMCPLink(goaipackage.WithMCPLink("./mcp_server/server.go"))

client, tools, err := goaipackage.ConnectAndLoadKnownTools(ctx, link)
```

Fungsi ini otomatis melakukan:
1. Membuka koneksi MCP client (`SSE` atau `Stdio`).
2. Mengambil daftar tool yang tersedia dari MCP server.
3. Mendaftarkan tool ke registry internal package untuk validasi tool call.

> [!NOTE]
> Pemangkasan token/kompresi deskripsi tool (*Caveman compression*) sudah berjalan **otomatis** di dalam `CallTools` / `ExecuteAgentTools`, sehingga pengguna tidak perlu memproses atau memangkas katalog tool secara manual.

---

### Toleransi Penamaan ID & Resolusi Otomatis (Identifier Engine)

Developer **tidak perlu khawatir atau bingung** memikirkan aturan kaku konvensi penamaan ID saat membuat tool MCP maupun merancang skema database. Package ini dilengkapi mesin resolusi cerdas (*Identifier Engine*) yang memberikan kebebasan dan fleksibilitas penuh.

---

#### Aturan A — Penamaan Parameter ID (di Tool MCP / Schema)

Mesin mengenali sebuah parameter sebagai "parameter ID" berdasarkan bentuk namanya. Berikut seluruh bentuk yang didukung:

**1. Exact match (parameter itu sendiri adalah ID):**

| Nama Parameter | Dikenali? |
|---|---|
| `id` | ✅ |
| `ID` | ✅ |
| `_id` | ✅ |
| `id_` | ✅ |

**2. Suffix — ID di belakang nama entitas:**

| Pola | Contoh | Dikenali? | Entitas yang diekstrak |
|---|---|---|---|
| `entitas_id` | `user_id`, `asset_id`, `id_transaksi` | ✅ | `user`, `asset` |
| `entitasId` | `userId`, `assetId` | ✅ | `user`, `asset` |
| `entitasID` | `userID`, `assetID` | ✅ | `user`, `asset` |
| `entitasiD` | `useriD`, `assetiD` | ✅ | `user`, `asset` |
| `ENTITAS_ID` | `USER_ID`, `ASSET_ID` | ✅ | `user`, `asset` |

> [!NOTE]
> `userid` (huruf kecil semua, tanpa underscore/kapital) **tidak dikenali** karena ambigu dengan kata biasa seperti `valid`, `avoid`. Gunakan minimal satu pemisah kapital atau underscore.

**3. Prefix — ID di depan nama entitas:**

| Pola | Contoh | Dikenali? | Entitas yang diekstrak |
|---|---|---|---|
| `id_entitas` | `id_user`, `id_asset`, `id_transaksi_pembelian` | ✅ | `user`, `asset`, `transaksi_pembelian` |
| `ID_ENTITAS` | `ID_USER`, `ID_ASSET` | ✅ | `user`, `asset` |
| `_id_entitas` | `_id_user`, `_id_asset` | ✅ | `user`, `asset` |
| `idEntitas` | `idUser`, `idAsset` | ✅ | `User`, `Asset` |
| `IDEntitas` | `IDUser`, `IDAsset` | ✅ | `User`, `Asset` |
| `IdEntitas` | `IdUser`, `IdAsset` | ✅ | `User`, `Asset` |
| `iDEntitas` | `iDUser`, `iDAsset` | ✅ | `User`, `Asset` |

> [!TIP]
> **Tidak ada penalti beda nama!** Jika parameter di tool MCP Anda bernama `id_asset`, tetapi kolom di database backend Anda bernama `assetId` atau `asset_id` (atau sebaliknya), mesin [`inferIdField`](file:///c:/Users/user/OneDrive/Documents/proyek_DH/QTERA/go_ai_package/identifier.go#L438) secara otomatis menjembatani dan mencocokkan kedua nama kolom tersebut tanpa konfigurasi manual.

---

#### Aturan B — Format Nilai ID (di Database / API Response)

Setelah parameter dikenali sebagai ID, mesin [`looksCanonicalID`](file:///c:/Users/user/OneDrive/Documents/proyek_DH/QTERA/go_ai_package/identifier.go#L309) memeriksa apakah nilai yang diberikan AI sudah berbentuk ID resmi (bukan nama bebas). Mesin ini belajar dari pola ID yang sudah ada di cache data referensi.

**Format yang dikenali otomatis:**

| Format | Contoh Nilai | Syarat Lolos | Dikenali? |
|---|---|---|---|
| **Angka murni** | `1`, `42`, `1001`, `00123` | Semua record juga pure digit | ✅ |
| **UUID** | `550e8400-e29b-41d4-a716-446655440000` | Minimal 1 record juga UUID | ✅ |
| **Prefix kode ≥ 2 char** | `id123`, `AST-001`, `ubm999`, `INV/2026/01`, `SKU-XYZ` | Record berbagi prefix ≥ 2 karakter (non-digit) | ✅ |
| **Suffix kode ≥ 2 char** | `123-id`, `001-AST`, `999TX` | Record berbagi suffix ≥ 2 karakter (non-digit) | ✅ |
| **1 karakter prefix/suffix** | `a1`, `1b` | Terlalu ambigu, ditolak | ❌ |
| **Nama bebas** | `meja rapat jati`, `Dell XPS` | Bukan pola ID | ❌ → fuzzy match |

**Logika deteksi prefix/suffix:**

Mesin menganalisis semua ID di cache data referensi untuk menemukan **prefix atau suffix yang sama**, kemudian memeriksa apakah nilai yang diberikan cocok dengan pola tersebut:

```
Records: ["id001", "id002", "id003"]
  → Common prefix: "id00" → strip angka di ujung → "id"
  → len("id") = 2 ≥ 2 ✅
  → "id123" starts with "id" → LOLOS ✅

Records: ["AST-001", "AST-002"]
  → Common prefix: "AST-00" → strip angka → "AST-"
  → len("AST-") = 4 ≥ 2 ✅
  → "AST-999" starts with "AST-" → LOLOS ✅

Records: ["001-TX", "002-TX"]
  → Common suffix: "0-TX" → strip angka kiri → "-TX"
  → len("-TX") = 3 ≥ 2 ✅
  → "999-TX" ends with "-TX" → LOLOS ✅

Records: ["a1", "a2"]
  → Common prefix: "a" → len("a") = 1 < 2 → DITOLAK ❌
```

> [!IMPORTANT]
> Nilai yang **tidak lolos** `looksCanonicalID` tidak langsung ditolak — sistem masih mencoba **fuzzy match** (mencocokkan dengan nama/label di data referensi). Hanya jika fuzzy match juga gagal, resolver tool baru diinjeksi ulang untuk mengambil data terbaru.

---

#### Aturan C — Resolusi Nama ke ID Otomatis (*Fuzzy Auto-Correction*)

Pengguna akhir di UI chat tidak perlu mengingat atau menghafal nomor ID teknis. Jika user menyebut nama barang atau entitas:
* User chat: *"Tolong hapus meja rapat jati"*
* AI memanggil: `delete_asset(id="meja rapat jati")` *(bukan ID resmi)*
* Sistem otomatis mencari ke data referensi sebelumnya, mencocokkan *"meja rapat jati"*, dan mengoreksi parameternya menjadi `id: "AST-042"`.

#### Aturan D — Proteksi Keamanan Data Ambigu (*Ambiguity Guard*)

Jika pengguna menyebut nama yang memiliki beberapa varian mirip di database (misal user minta update *"Dell"* padahal ada *"Dell Inspiron"* dan *"Dell XPS"*):
* Pada operasi baca (*Read*), sistem memilih hasil yang paling relevan.
* Pada operasi mutasi data (*Write / Delete / Update*), sistem **secara otomatis menolak menebak sendiri** demi mencegah terjadinya salah edit atau salah hapus data penting.

---

## 4. RBAC (Role-Based Access Control)

Mengontrol tool mana yang boleh dipanggil oleh role tertentu.

### `SetRBACMap(rules map[string][]string)`

```go
goaipackage.SetRBACMap(map[string][]string{
    "admin":  {"list_assets", "create_asset", "delete_asset"},
    "user":   {"list_assets"},
    "guest":  {}, // tidak boleh memanggil tool apapun
})
```

### `SetRBACRules(rbac RBACRule)`

Versi struct-based dari `SetRBACMap`.

```go
rbac := goaipackage.NewRBACRule(
    goaipackage.WithRBACMap(map[string][]string{
        "admin": {"list_assets", "create_asset"},
    }),
)
goaipackage.SetRBACRules(rbac)
```

> [!IMPORTANT]
> Role string bersifat **case-insensitive** dan di-trim otomatis. `"Admin"`, `"ADMIN"`, `"admin"` dianggap sama.
> Jika role tidak ada di peta RBAC, `CallTools` langsung mengembalikan pesan "tidak punya izin".

---

## 5. Database Riwayat Chat

Library menyertakan **SQLite Pure-Go** (`modernc.org/sqlite`) — tidak butuh CGO/GCC.

### `SetDBPath(path string)`

Ubah lokasi file SQLite sebelum repository pertama dibuat. Default: `data/chat_history.db`.

```go
goaipackage.SetDBPath("./storage/chat.db")
```

---

### `NewHistoryRepository() (*HistoryRepository, error)`

Buat instance repository. Tabel & index dibuat otomatis jika belum ada.

```go
repo, err := goaipackage.NewHistoryRepository()
if err != nil {
    log.Fatal(err)
}
defer goaipackage.CloseHistoryDB()
```

---

### `repo.LoadContext(req *RequestChat) (map[string]any, error)`

Ambil konteks sesi: 10 pesan riwayat chat terbaru + dokumen lampiran aktif.

```go
userCtx, err := repo.LoadContext(&goaipackage.RequestChat{
    CacheID: "user-session-123",
    Auth:    goaipackage.UserAuth{Role: "admin", UserID: "u001"},
})
// userCtx["history"] → []ChatMessage
// userCtx["attachment_text"] → string (jika ada lampiran sebelumnya)
```

---

### `repo.SaveTurn(cacheID, userID, role, message, attachBool, filePath, fileName, extractedText) error`

Simpan satu giliran percakapan ke database.

| Parameter | Keterangan |
|---|---|
| `cacheID` | ID sesi unik (contoh: cookie session ID) |
| `userID` | ID pengguna |
| `role` | `"user"` untuk pertanyaan, `"assistant"` untuk jawaban bot |
| `message` | Isi teks pesan |
| `attachBool` | `true` jika ada lampiran di giliran ini |
| `filePath` | Path file lampiran (kosongkan jika tidak ada) |
| `fileName` | Nama file lampiran |
| `extractedText` | Teks hasil ekstraksi dari file lampiran |

```go
// Simpan pertanyaan user
repo.SaveTurn(cacheID, userID, "user", req.Message, false, "", "", "")

// Simpan jawaban bot
repo.SaveTurn(cacheID, userID, "assistant", answer, false, "", "", "")
```

---

### `CloseHistoryDB() error`

Tutup koneksi SQLite. Panggil saat aplikasi shutdown.

```go
defer goaipackage.CloseHistoryDB()
```

---

## 6. Ekstraksi Dokumen & OCR

### `ExtractText(content []byte, filename string) (string, error)`

Router otomatis — deteksi format dari nama file dan panggil extractor yang sesuai.

| Ekstensi | Method |
|---|---|
| `.pdf` | Ekstraksi teks PDF |
| `.xlsx`, `.xls` | Ekstraksi Excel (semua sheet) |
| `.csv` | Parsing CSV |
| `.png`, `.jpg`, `.jpeg`, `.webp` | OCR via Tesseract |
| Lainnya | Dikembalikan as-is sebagai string |

```go
data, _ := os.ReadFile("laporan.pdf")
text, err := goaipackage.ExtractText(data, "laporan.pdf")
```

---

### `ExtractExcelText(content []byte) (string, error)`

Ekstraksi semua sheet Excel menjadi teks tabular.

```go
data, _ := os.ReadFile("data.xlsx")
text, err := goaipackage.ExtractExcelText(data)
```

---

### `ExtractCSVText(content []byte) (string, error)`

Parsing CSV menjadi format `[Headers]: col1 | col2` dan `Row N: val1 | val2`.

---

### `ExtractImageOCR(content []byte, filename string) (string, error)`

OCR gambar menggunakan Tesseract. Memerlukan Tesseract terinstall di sistem.

Proses otomatis:
1. Preprocessing gambar dengan High-Pass Filter (tajamkan teks)
2. Tesseract dengan bahasa `eng+ind`, fallback ke bahasa default

```go
data, _ := os.ReadFile("nota.png")
text, err := goaipackage.ExtractImageOCR(data, "nota.png")
```

> [!NOTE]
> Install Tesseract: `winget install UB-Mannheim.TesseractOCR` (Windows) atau `apt install tesseract-ocr` (Linux).

---

## 7. Tipe Data & Functional Options

### Tipe Data Utama

```go
// Identitas pengguna
type UserAuth struct {
    Role      string
    UserID    string
    CompanyID string
    Name      string
}

// Pesan riwayat chat
type ChatMessage struct {
    Role string // "user" atau "assistant"
    Chat string
}

// Pesan untuk LLM
type Message struct {
    Role    string // "system", "user", "assistant"
    Content string
}

// Hasil CallTools
type AgentResult struct {
    Context     string               // Fakta yang diekstrak
    Attachment  *AttachmentInfo      // File yang dihasilkan (jika ada)
    ToolResults []ToolExecutionResult
}

// Hasil GenerateResponse
type ResponseResult struct {
    Answer string
}
```

---

### Functional Options

#### Intent Options
```go
// Tambahkan instruksi kustom ke prompt intent classifier
goaipackage.WithIntentPrompt("Gunakan tool search_product untuk semua query produk.")
```

#### Tools Options
```go
// Guardrail kustom untuk tool calling loop
goaipackage.WithToolsPrompt("Jangan memanggil tool mutasi tanpa konfirmasi eksplisit user.")
```

#### Response Options
```go
// Override persona/gaya jawaban AI
goaipackage.WithResponsePrompt("Kamu adalah Ara. Jawab dalam Bahasa Indonesia, singkat, dan profesional.")
```

#### RBAC Options
```go
goaipackage.WithRBACMap(map[string][]string{
    "admin": {"tool_a", "tool_b"},
})
```

#### MCP Link Options
```go
goaipackage.WithMCPLink("http://localhost:3000/sse")
goaipackage.WithMCPLink("./mcp_server/server.go")
```

---

### Helper

#### `ToUserAuth(v any, defaultRole ...string) UserAuth`

Konversi `map[string]any`, struct `UserAuth`, atau nested auth object ke `UserAuth` yang ternormalisasi. Otomatis mengenali berbagai variasi penamaan key (`company_id`/`company`/`tenant_id`, `user_id`/`UserID`, `name`/`username`, `role`/`Role`).

```go
auth := goaipackage.ToUserAuth(map[string]any{
    "role":       "admin",
    "user_id":    "u001",
    "company_id": "PT Maju",
})
```

#### `FormatChatHistoryForLlm(history []ChatMessage) string`

Format slice riwayat chat menjadi blok teks dialog untuk prompt LLM.

```go
dialogText := goaipackage.FormatChatHistoryForLlm(history)
// → "user: Halo\nassistant: Halo juga!"
```

---

## 8. Contoh Alur Lengkap

### REST Server Multi-Turn Chat

```go
package main

import (
    "context"
    "encoding/json"
    "log"
    "net/http"

    goai "github.com/your-org/go_ai_package"
)

func main() {
    ctx := context.Background()

    // 1. Kredensial LLM
    goai.SetAIConfig("sk-or-v1-xxx", "https://openrouter.ai/api/v1")

    // 2. RBAC
    goai.SetRBACMap(map[string][]string{
        "admin": {"list_assets", "create_asset", "delete_asset"},
        "user":  {"list_assets"},
    })

    // 3. Database riwayat chat
    repo, err := goai.NewHistoryRepository()
    if err != nil {
        log.Fatal(err)
    }
    defer goai.CloseHistoryDB()

    // 4. Koneksi MCP server
    mcpClient, tools, err := goai.ConnectAndLoadKnownTools(ctx,
        goai.NewMCPLink(goai.WithMCPLink("http://localhost:3000/sse")),
    )
    if err != nil {
        log.Fatal(err)
    }

    // 5. HTTP handler
    http.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            Message string `json:"message"`
            Role    string `json:"role"`
            CacheID string `json:"cache_id"`
        }
        json.NewDecoder(r.Body).Decode(&req)

        // A. Muat riwayat sesi dari DB
        userCtx, _ := repo.LoadContext(&goai.RequestChat{
            CacheID: req.CacheID,
            Auth:    goai.UserAuth{Role: req.Role},
        })

        // B. Jalankan agen CallTools & GenerateResponse
        // (model "" = pakai DefaultModel: liquid/lfm-2.5-2.6b:free)
        toolJSON, _ := goai.CallTools(
            r.Context(), mcpClient, tools,
            req.Message, userCtx, 3, "",
        )

        respJSON, _ := goai.GenerateResponse(
            r.Context(),
            req.Message, toolJSON, userCtx, "",
            goai.WithResponsePrompt("Kamu adalah Ara, asisten aset perusahaan. Jawab profesional."),
        )

        // C. Parse jawaban
        var out struct{ Answer string `json:"answer"` }
        json.Unmarshal([]byte(respJSON), &out)

        // D. Simpan ke database untuk sesi berikutnya
        repo.SaveTurn(req.CacheID, req.Role, "user",      req.Message, false, "", "", "")
        repo.SaveTurn(req.CacheID, req.Role, "assistant", out.Answer,  false, "", "", "")

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]any{"answer": out.Answer})
    })

    log.Println("Server berjalan di :8080")
    http.ListenAndServe(":8080", nil)
}
```

---

### Chat dengan Upload Dokumen (PDF/Excel/Gambar)

```go
http.HandleFunc("/api/chat-with-file", func(w http.ResponseWriter, r *http.Request) {
    r.ParseMultipartForm(32 << 20)
    message := r.FormValue("message")
    cacheID := r.FormValue("cache_id")
    role    := r.FormValue("role")

    // Baca file yang diupload
    var attachText, attachName string
    if file, header, err := r.FormFile("file"); err == nil {
        defer file.Close()
        data, _ := io.ReadAll(file)
        attachName = header.Filename
        attachText, _ = goai.ExtractText(data, attachName) // otomatis deteksi format
    }

    // Muat konteks sesi
    userCtx, _ := repo.LoadContext(&goai.RequestChat{
        CacheID: cacheID,
        Auth:    goai.UserAuth{Role: role},
    })

    // Inject teks dokumen ke konteks
    if attachText != "" {
        userCtx["attachment_text"] = attachText
        userCtx["attachment_name"] = attachName
        userCtx["has_user_uploaded_file"] = true
    }

    toolJSON, _ := goai.CallTools(r.Context(), mcpClient, tools, message, userCtx, 3, "")
    respJSON, _ := goai.GenerateResponse(r.Context(), message, toolJSON, userCtx, "")

    var out struct{ Answer string `json:"answer"` }
    json.Unmarshal([]byte(respJSON), &out)

    // Simpan ke DB (dengan metadata lampiran)
    repo.SaveTurn(cacheID, role, "user",      message,    true, "", attachName, attachText)
    repo.SaveTurn(cacheID, role, "assistant", out.Answer, false, "", "", "")

    json.NewEncoder(w).Encode(map[string]any{"answer": out.Answer})
})
```
