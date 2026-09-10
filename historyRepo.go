package goaipackage // Gunakan nama package (bukan main) jika ingin dijadikan plugin/library

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // driver pure-Go, tidak butuh CGO/gcc di mesin pemakai
)

// =====================================================================
// Internal DB management — privat, tidak diekspor. Pemakai library tidak
// lagi perlu sql.Open(), bikin tabel manual, atau import driver sendiri.
// =====================================================================

const defaultDBPath = "data/chat_history.db"

const schemaSQL = `
CREATE TABLE IF NOT EXISTS chat_history (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	cache_id       TEXT NOT NULL,
	user_id        TEXT,
	role           TEXT NOT NULL,
	message        TEXT NOT NULL,
	attach_bool    INTEGER NOT NULL DEFAULT 0,
	file_path      TEXT,
	file_name      TEXT,
	extracted_text TEXT,
	created_at     DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chat_history_cache_id ON chat_history (cache_id);
`

var (
	sharedDB *sql.DB
	dbPath   = defaultDBPath
	initOnce sync.Once
	initErr  error
)

// SetDBPath mengganti lokasi file SQLite. Opsional — panggil sekali,
// sebelum repository pertama dibuat, kalau tidak mau pakai default
// "data/chat_history.db". Kalau tidak dipanggil sama sekali pun tetap
// jalan: library pakai path default dan bikin foldernya sendiri.
func SetDBPath(path string) {
	if strings.TrimSpace(path) != "" {
		dbPath = path
	}
}

// openDB lazy-init: buka koneksi shared + jalankan migrasi skema hanya
// sekali, di panggilan pertama. Tidak pernah diekspos ke luar package.
func openDB() (*sql.DB, error) {
	initOnce.Do(func() {
		if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				initErr = fmt.Errorf("history: gagal membuat folder db: %w", err)
				return
			}
		}
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			initErr = fmt.Errorf("history: gagal membuka sqlite: %w", err)
			return
		}
		if _, err := db.Exec(schemaSQL); err != nil {
			db.Close()
			initErr = fmt.Errorf("history: gagal migrasi skema: %w", err)
			return
		}
		sharedDB = db
	})
	return sharedDB, initErr
}

// CloseHistoryDB menutup koneksi SQLite yang dipakai bersama. Panggil
// sekali saat shutdown (mis. lewat defer di main). Aman dilewati kalau
// proses langsung exit.
func CloseHistoryDB() error {
	if sharedDB != nil {
		return sharedDB.Close()
	}
	return nil
}

// =====================================================================
// Public repository — method-nya sama persis seperti sebelumnya, yang
// berubah cuma cara membuatnya.
// =====================================================================

type HistoryRepository struct {
	db *sql.DB
}

// NewHistoryRepository sekarang tidak butuh *sql.DB sama sekali. Koneksi
// dan migrasi tabel ditangani sendiri oleh library di panggilan pertama.
func NewHistoryRepository() (*HistoryRepository, error) {
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	return &HistoryRepository{db: db}, nil
}

// LoadContext mengambil riwayat chat dan teks lampiran aktif berdasarkan CacheID
func (repo *HistoryRepository) LoadContext(req *RequestChat) (map[string]any, error) {
	uctx := map[string]any{
		"user_id":  req.Auth.UserID,
		"username": req.Auth.Name,
		"role":     req.Auth.Role,
		"company":  req.Auth.CompanyID,
	}

	// 1. Ambil Riwayat Chat (History) 10 pesan terakhir
	historyQuery := `
		SELECT role, message FROM (
			SELECT id, role, message 
			FROM chat_history 
			WHERE cache_id = ? 
			ORDER BY id DESC LIMIT 10
		) ORDER BY id ASC`

	rows, err := repo.db.Query(historyQuery, req.CacheID)
	if err == nil {
		defer rows.Close()
		var history []ChatMessage
		for rows.Next() {
			var role, msg string
			if err := rows.Scan(&role, &msg); err == nil {
				history = append(history, ChatMessage{
					Role: role,
					Chat: msg,
				})
			}
		}
		if len(history) > 0 {
			uctx["history"] = history
		}
	}

	// 2. Ambil Konteks Attachment
	if req.Attach && req.LinkAttach != "" && req.LinkAttach != "-" {
		filename := filepath.Base(req.LinkAttach)
		uctx["attachment_name"] = filename
		uctx["has_user_uploaded_file"] = true
	} else {
		attachQuery := `
			SELECT file_name, extracted_text 
			FROM chat_history 
			WHERE cache_id = ? AND attach_bool = 1 AND extracted_text IS NOT NULL AND extracted_text != ''
			ORDER BY id DESC LIMIT 1`

		var attachName, attachText string
		err := repo.db.QueryRow(attachQuery, req.CacheID).Scan(&attachName, &attachText)
		if err == nil && strings.TrimSpace(attachText) != "" {
			uctx["attachment_name"] = attachName
			uctx["attachment_text"] = attachText
			uctx["has_user_uploaded_file"] = false
		}
	}

	return uctx, nil
}

// SaveTurn menyimpan pertanyaan user dan respon bot ke SQLite
func (repo *HistoryRepository) SaveTurn(cacheID, userID, role, message string, attachBool bool, filePath, fileName, extractedText string) error {
	query := `
		INSERT INTO chat_history (cache_id, user_id, role, message, attach_bool, file_path, file_name, extracted_text)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	isAttach := 0
	if attachBool {
		isAttach = 1
	}

	_, err := repo.db.Exec(query, cacheID, userID, role, message, isAttach, filePath, fileName, extractedText)
	return err
}
