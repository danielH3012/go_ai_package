# Dokumentasi `go_ai_package`

Library AI Agent Go modular untuk membangun chatbot berbasis MCP (Model Context Protocol) dengan dukungan multi-turn session, RBAC, dan ekstraksi dokumen.

---

## Daftar Isi

1. [Konfigurasi Global](#1-konfigurasi-global)
2. [Tiga Agen Utama AI](#2-tiga-agen-utama-ai)
3. [Koneksi MCP & Tool Execution](#3-koneksi-mcp--tool-execution)
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

| Key | Isi |
|---|---|
| `"history"` | `[]ChatMessage` — 10 pesan terakhir sesi |
| `"role"` | `string` — role pengguna |
| `"user_id"` | `string` — ID pengguna |
| `"company"` | `string` — nama perusahaan |
| `"attachment_text"` | `string` — teks dokumen yang dilampirkan |
| `"attachment_name"` | `string` — nama file lampiran |
| `"has_user_uploaded_file"` | `bool` — apakah ada upload baru |

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

Menjalankan reasoning loop multi-turn: pilih tool → eksekusi tool (paralel) → ekstrak fakta → ulangi jika data belum cukup. Menegakkan RBAC secara otomatis.

```go
func CallTools(
    ctx           context.Context,
    client        *client.Client,
    mcpTools      []mcp.Tool,
    query         string,
    role          string,
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
| `role` | Role user untuk filter RBAC (contoh: `"admin"`, `"user"`) |
| `maxIterations` | Batas iterasi loop (minimal 3, lebih besar = lebih teliti) |

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
    "Tampilkan 5 aset termahal", "admin",
    userCtx, 3, "",
)
```

**Custom guardrail**:
```go
toolJSON, err := goaipackage.CallTools(
    ctx, mcpClient, mcpTools, query, role, userCtx, 3, "",
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

Konversi `map[string]any` atau struct ke `UserAuth`. Berguna saat parsing JWT claims atau session data.

```go
auth := goaipackage.ToUserAuth(map[string]any{
    "role":    "admin",
    "user_id": "u001",
    "company": "PT Maju",
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

        // B. Jalankan tiga agen secara berurutan
        // (model "" = pakai DefaultModel: liquid/lfm-2.5-2.6b:free)
        toolJSON, _ := goai.CallTools(
            r.Context(), mcpClient, tools,
            req.Message, req.Role, userCtx, 3, "",
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

    toolJSON, _ := goai.CallTools(r.Context(), mcpClient, tools, message, role, userCtx, 3, "")
    respJSON, _ := goai.GenerateResponse(r.Context(), message, toolJSON, userCtx, "")

    var out struct{ Answer string `json:"answer"` }
    json.Unmarshal([]byte(respJSON), &out)

    // Simpan ke DB (dengan metadata lampiran)
    repo.SaveTurn(cacheID, role, "user",      message,    true, "", attachName, attachText)
    repo.SaveTurn(cacheID, role, "assistant", out.Answer, false, "", "", "")

    json.NewEncoder(w).Encode(map[string]any{"answer": out.Answer})
})
```
