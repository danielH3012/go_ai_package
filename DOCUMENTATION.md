# Dokumentasi Teknis `go_ai_package`

Dokumentasi lengkap seluruh fungsi, tipe data, variabel, mekanisme memori multi-turn, dan cara penggunaan library AI Agent Go modular (`package goaipackage`).

---

## 1. Konfigurasi Global & LLM Client (`models.go`)

### `SetAIConfig(apiKey string, baseURL string)`
Mengatur kredensial LLM secara terprogram tanpa ketergantungan file `.env`.
- **Parameter**:
  - `apiKey`: Token/API key penyedia LLM (DeepSeek, OpenRouter, OpenAI, Groq, dll.).
  - `baseURL`: Endpoint API LLM (contoh: `https://api.deepseek.com`, `https://openrouter.ai/api/v1`, atau `https://api.openai.com/v1`).
- **Contoh**:
  ```go
  // Contoh menggunakan endpoint resmi DeepSeek
  goaipackage.SetAIConfig("sk-xxx", "https://api.deepseek.com")

  // Contoh menggunakan OpenRouter
  goaipackage.SetAIConfig("sk-or-v1-xxx", "https://openrouter.ai/api/v1")
  ```

> [!TIP]
> **Pemilihan Nama Model**:
> - Jika menggunakan **DeepSeek / OpenAI langsung**, gunakan nama model resmi penyedia seperti `"deepseek-chat"` atau `"gpt-4o-mini"`.
> - Jika menggunakan **OpenRouter**, gunakan format dengan vendor prefix seperti `"deepseek/deepseek-chat"` atau biarkan kosong `""` untuk memakai default catalog.

### `SetAPIKey(apiKey string)` & `SetBaseURL(baseURL string)`
Mengatur API Key atau Base URL secara terpisah.

### `ChatGenerate(ctx, messages, tools, maxNewTokens, model, retries)`
Fungsi inti pemanggilan completion LLM dengan retry dan exponential backoff.
- **Parameter**:
  - `ctx`: `context.Context` untuk timeout/cancellation.
  - `messages`: Slice `[]Message` (Role: system/user/assistant, Content: string).
  - `tools`: Skema tool (opsional, `nil` jika tanpa tool calling native).
  - `maxNewTokens`: Batas token output (default: 4096).
  - `model`: Nama model AI.
  - `retries`: Jumlah pengulangan jika gagal (rekomendasi: 2–3).
- **Return**: `(*ChatResult, error)`

### `SanitizeModelReply(raw string) string`
Membersihkan teks mentah LLM dari tag `<thought>`, `<think>`, token DSML, atau sisa payload JSON.

---

## 2. Tiga Agen Utama AI & Alur Multi-Turn (`models.go`)

Semua agen utama menghasilkan **output JSON string** (`(string, error)`) secara langsung agar mudah di-parse atau diteruskan ke REST API / frontend tanpa ketergantungan struct Go.

### Mekanisme Memori Konteks (`userContext`)
Ketika `userContext` yang memuat riwayat percakapan (`userContext["history"]`) diberikan ke agen:
- **`SearchIntent`**: Otomatis menyuntikkan riwayat chat ke dalam prompt sehingga mengenali pertanyaan rujukan/kontekstual (misalnya pengguna bertanya *"bandingkan performanya"* tanpa menyebut ulang nama barang).
- **`CallTools`**: Memanfaatkan riwayat chat untuk menyaring data yang relevan dalam loop reasoning.
- **`GenerateResponse`**: Otomatis menyuntikkan `=== PREVIOUS CONVERSATION HISTORY ===` ke prompt LLM, sehingga AI dapat menjawab pertanyaan perbandingan atau mengingat rekomendasi dari giliran sebelumnya tanpa amnesia.

---

### Agen 1: `SearchIntent`
Menganalisis kebutuhan tool, klasifikasi kategori, dan mendeteksi apakah pertanyaan relevan atau off-topic.
- **Fungsi**:
  - `SearchIntent(ctx, query, userContext, model, intentOpts...) (string, error)`
- **Parameter**:
  - `ctx`: `context.Context`.
  - `query`: Pertanyaan/instruksi user.
  - `userContext`: Map konteks sesi chat (`map[string]any`) yang didapat dari `repo.LoadContext()`.
  - `model`: Model LLM (misal: `"deepseek-chat"`).
  - `intentOpts`: Opsi kustom prompt (misal: `WithIntentPrompt("...")`).
- **Return**: `(string, error)` — JSON format `{"tools": [...], "reason": "...", "target_tool": "...", "category": "...", "is_off_topic": false}`.
- **Contoh**:
  ```go
  intentJSON, err := goaipackage.SearchIntent(ctx, "Tampilkan laptop termurah", userCtx, "deepseek-chat")
  ```

---

### Agen 2: `CallTools`
Menjalankan reasoning loop multi-turn, verifikasi hak akses RBAC, pemanggilan tool MCP paralel, dan ekstraksi fakta dari hasil tool.
- **Fungsi**:
  - `CallTools(ctx, client, mcpTools, query, role, userContext, maxIterations, model, toolsOpts...) (string, error)`
- **Parameter**:
  - `ctx`: `context.Context`.
  - `client`: `*client.Client` koneksi MCP server.
  - `mcpTools`: Daftar tool MCP `[]mcp.Tool`.
  - `query`: Pertanyaan user.
  - `role`: Role pengguna untuk verifikasi RBAC (misal: `"admin"`, `"purchaser"`, `"guest"`).
  - `userContext`: Map konteks sesi chat (`map[string]any`).
  - `maxIterations`: Batas iterasi loop perbaikan (minimal: 3).
  - `model`: Model LLM.
  - `toolsOpts`: Opsi guardrails kustom (misal: `WithToolsPrompt("...")`).
- **Return**: `(string, error)` — JSON format `{"context": "...", "attachment": {...}, "tool_results": [...]}`.
- **Contoh**:
  ```go
  toolJSON, err := goaipackage.CallTools(ctx, mcpClient, mcpTools, query, "purchaser", userCtx, 3, "deepseek-chat")
  ```

---

### Agen 3: `GenerateResponse`
Menyusun jawaban akhir berbahasa manusiawi berdasarkan data konteks hasil tool dan riwayat percakapan sebelumnya.
- **Fungsi**:
  - `GenerateResponse(ctx, query, contextStr, userContext, model, respOpts...) (string, error)`
- **Parameter**:
  - `ctx`: `context.Context`.
  - `query`: Pertanyaan user saat ini.
  - `contextStr`: Data konteks hasil `CallTools` (dapat langsung memasukkan raw string `toolJSON` dari `CallTools`).
  - `userContext`: Map konteks tambahan dari `repo.LoadContext()` (berisi `history`, `attachment_text`, dll.).
  - `model`: Model LLM.
  - `respOpts`: Opsi kustom instruksi persona atau sistem respon (misal: `WithResponsePrompt("...")`).
- **Return**: `(string, error)` — JSON format `{"answer": "..."}`.
- **Contoh**:
  ```go
  respPrompt := "Anda adalah Aura, asisten purchasing Tokopedia. Jawab dengan ramah dan informatif."
  respJSON, err := goaipackage.GenerateResponse(ctx, query, toolJSON, userCtx, "deepseek-chat",
      goaipackage.WithResponsePrompt(respPrompt),
  )
  ```

---

## 3. Struktur Data & Opsi (`types.go`)

### Model Otentikasi & Konteks
- `UserAuth`: Struct identitas pengguna (`Role`, `UserID`, `CompanyID`, `Name`).
- `ChatMessage`: Struct pesan riwayat (`Role`, `Chat`).
- `FormatChatHistoryForLlm(history []ChatMessage) string`: Mengubah slice riwayat pesan menjadi blok teks dialog yang rapi untuk prompt LLM.

### Custom Guardrails & Functional Options
- **Intent**: `WithIntentPrompt(prompt string)` / `NewIntentPrompt(opts...)`
- **Tools**: `WithToolsPrompt(prompt string)`, `WithToolsList(tools []string)` / `NewToolsPrompt(opts...)`
- **Response**: `WithResponsePrompt(prompt string)` / `NewResponsePrompt(opts...)`
- **RBAC**: `WithRBACMap(rules map[string][]string)` / `NewRBACRule(opts...)`
- **MCP Link**: `WithMCPLink(link string)` / `NewMCPLink(opts...)`

---

## 4. Koneksi MCP & Eksekusi Tool (`toolCall.go`)

### `SetRBACMap(rules map[string][]string)` / `SetRBACRules(rbac RBACRule)`
Mengatur daftar tool yang diizinkan untuk dieksekusi per role pengguna.
- **Contoh**:
  ```go
  goaipackage.SetRBACMap(map[string][]string{
      "admin":     {"search_tokopedia", "tokopedia_raw_gql"},
      "purchaser": {"search_tokopedia"},
      "guest":     {}, // Role tamu tanpa izin memanggil tools
  })
  ```

### `ConnectAndLoadKnownTools(ctx, mcpLink)`
Menghubungkan ke MCP server via SSE URL (`http://...`) atau file skrip executable/stdio (`../mcp/server.go`), mendaftarkan tool secara otomatis.
- **Return**: `(*client.Client, []mcp.Tool, error)`

### `FetchMCPTools(ctx, client)`
Mengambil daftar tool aktif dari MCP Server.

### `ShrinkToolCatalog(tools []mcp.Tool) []mcp.Tool`
Memangkas deskripsi tool menggunakan minifier native Go atau CLI `caveman-shrink` untuk menghemat token prompt.

---

## 5. Database Riwayat Chat & Multi-Turn Cache (`historyRepo.go`)

Menggunakan SQLite Pure-Go (`modernc.org/sqlite`) tanpa membutuhkan compiler C / GCC (CGO-free).

### `SetDBPath(path string)`
Mengatur lokasi file database SQLite (default: `data/chat_history.db`).

### `NewHistoryRepository()`
Membuat instance repository dan otomatis menginisialisasi tabel `chat_history`.
- **Return**: `(*HistoryRepository, error)`.

### `repo.LoadContext(req *RequestChat)`
Mengambil **10 pesan riwayat chat terbaru** dalam urutan kronologis yang benar berdasarkan `CacheID`, serta memuat dokumen aktif jika tersedia.
- **Parameter**: `req (*RequestChat)` (wajib mengisi `CacheID` dan `Auth.Role`).
- **Return**: `(map[string]any, error)` — berisi field `history` (`[]ChatMessage`), `role`, `user_id`, dll.

### `repo.SaveTurn(cacheID, userID, role, message, attachBool, filePath, fileName, extractedText)`
Menyimpan satu giliran percakapan ke database SQLite.
- **Parameter**:
  - `cacheID`: ID sesi percakapan unik pengguna (misal dari cookie / localStorage).
  - `userID`: Identitas pengguna (misal `"user"` atau email).
  - `role`: Peran pengirim (`"user"` untuk pertanyaan, `"assistant"` untuk respon bot).
  - `message`: Isi teks pesan.
  - `attachBool`: `true` jika pesan ini memiliki lampiran dokumen/file.
  - `filePath`, `fileName`, `extractedText`: Detail metadata lampiran jika ada.
- **Return**: `error`.

### `CloseHistoryDB()`
Menutup koneksi SQLite secara aman saat aplikasi shutdown.

---

## 6. Ekstraksi Dokumen & OCR (`extract.go`)

- `ExtractText(content []byte, filename string) (string, error)`: Ekstraksi otomatis dari format `.pdf`, `.xlsx`, `.xls`, `.csv`, `.png`, `.jpg`, `.jpeg`, `.webp`.
- `ExtractExcelText(content []byte) (string, error)`: Ekstraksi lembar kerja Excel menjadi format tabular teks.
- `ExtractCSVText(content []byte) (string, error)`: Ekstraksi CSV berstruktur kolom & baris.
- `ExtractImageOCR(content []byte, filename string) (string, error)`: OCR gambar menggunakan preprocessing High-Pass Filter dan engine Tesseract.

---

## 7. Resolver Model AI (`resolve.go`)

### `ResolveModel(name string) string`
Mengonversi alias pendek (`"deepseek"`, `"gpt"`, `"qwen"`, dll.) menjadi ID model spesifik di katalog OpenRouter.

---

## 8. Contoh Alur Lengkap: Server Multi-Turn Chat

Berikut contoh implementasi REST server multi-turn dengan session cache SQLite:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"

	"go_ai_package"
)

func main() {
	ctx := context.Background()

	// 1. Kredensial AI & RBAC
	goaipackage.SetAIConfig("sk-xxx", "https://api.deepseek.com")
	goaipackage.SetRBACMap(map[string][]string{
		"purchaser": {"search_tokopedia"},
		"guest":     {},
	})

	// 2. Inisialisasi Cache DB (Pure-Go SQLite)
	repo, err := goaipackage.NewHistoryRepository()
	if err != nil {
		log.Fatalf("Gagal inisialisasi DB: %v", err)
	}
	defer goaipackage.CloseHistoryDB()

	// 3. Hubungkan ke MCP Server Tokopedia
	mcpPath, _ := filepath.Abs("../mcp/server.go")
	client, tools, err := goaipackage.ConnectAndLoadKnownTools(ctx, goaipackage.NewMCPLink(goaipackage.WithMCPLink(mcpPath)))
	if err != nil {
		log.Fatalf("Gagal koneksi MCP: %v", err)
	}

	// 4. Chat Handler dengan Dukungan Multi-Turn Session Cache
	http.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Message string `json:"message"`
			Role    string `json:"role"`
			CacheID string `json:"cache_id"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		// A. Ambil riwayat chat sebelumnya dari cache_id
		userCtx, _ := repo.LoadContext(&goaipackage.RequestChat{
			CacheID: req.CacheID,
			Auth:    goaipackage.UserAuth{Role: req.Role},
		})

		// B. Eksekusi 3 Agen (History otomatis terbaca di Agen 1, 2, dan 3)
		intentJSON, _ := goaipackage.SearchIntent(r.Context(), req.Message, userCtx, "deepseek-chat")
		toolJSON, _ := goaipackage.CallTools(r.Context(), client, tools, req.Message, req.Role, userCtx, 3, "deepseek-chat")
		respJSON, _ := goaipackage.GenerateResponse(r.Context(), req.Message, toolJSON, userCtx, "deepseek-chat")

		var parsed struct{ Answer string `json:"answer"` }
		json.Unmarshal([]byte(respJSON), &parsed)

		// C. Simpan pertanyaan user dan respon AI ke database cache
		repo.SaveTurn(req.CacheID, "user", "user", req.Message, false, "", "", "")
		repo.SaveTurn(req.CacheID, "user", "assistant", parsed.Answer, false, "", "", "")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"answer": parsed.Answer})
	})

	log.Println("Server berjalan di http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}
```
