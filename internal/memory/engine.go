package memory

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

// Doc represents a stored memory document.
type Doc struct {
	ID        int64     `json:"id"`
	Category  string    `json:"category"`
	Topic     string    `json:"topic"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// QueryResult extends Doc with a relevance score from hybrid search.
type QueryResult struct {
	Doc
	Score float64 `json:"score"`
}

// Engine wraps the memory database and embedding client.
type Engine struct {
	db    *sql.DB
	embed *EmbedClient
}

// New opens the memory database for projectPath and returns an Engine.
func New(projectPath string) (*Engine, error) {
	db, err := Open(projectPath)
	if err != nil {
		return nil, err
	}
	return &Engine{db: db, embed: NewEmbedClient()}, nil
}

// Close releases the underlying database connection.
func (e *Engine) Close() error {
	return e.db.Close()
}

// SaveMemory persists a new document and its embedding.
// Returns the saved Doc (with assigned ID).
func (e *Engine) SaveMemory(category, topic, content string) (*Doc, error) {
	embedding, err := e.embed.Embed(category + " " + topic + " " + content)
	if err != nil {
		return nil, fmt.Errorf("save_memory: generate embedding: %w", err)
	}

	blob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("save_memory: serialize embedding: %w", err)
	}

	tx, err := e.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("save_memory: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.Exec(
		`INSERT INTO docs (category, topic, content) VALUES (?, ?, ?)`,
		category, topic, content,
	)
	if err != nil {
		return nil, fmt.Errorf("save_memory: insert doc: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("save_memory: get last insert id: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO docs_vec (id, embedding) VALUES (?, ?)`,
		id, blob,
	); err != nil {
		return nil, fmt.Errorf("save_memory: insert vec: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("save_memory: commit: %w", err)
	}

	doc := &Doc{ID: id, Category: category, Topic: topic, Content: content, Timestamp: time.Now()}
	return doc, nil
}

// QueryMemory performs a hybrid FTS5 + vector search and returns the top-k
// most relevant documents fused via Reciprocal Rank Fusion.
func (e *Engine) QueryMemory(query string, topK int) ([]QueryResult, error) {
	if topK <= 0 {
		topK = 3
	}

	// --- FTS5 keyword search ---
	ftsIDs, err := e.queryFTS(query, topK*5)
	if err != nil {
		return nil, fmt.Errorf("query_memory: fts search: %w", err)
	}

	// --- Vector search ---
	embedding, err := e.embed.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("query_memory: embed query: %w", err)
	}
	blob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("query_memory: serialize query embedding: %w", err)
	}
	vecIDs, err := e.queryVec(blob, topK*5)
	if err != nil {
		return nil, fmt.Errorf("query_memory: vec search: %w", err)
	}

	// --- RRF merge ---
	fused := MergeRRF(ftsIDs, vecIDs)
	if len(fused) > topK {
		fused = fused[:topK]
	}

	results := make([]QueryResult, 0, len(fused))
	for _, f := range fused {
		doc, err := e.getDoc(f.ID)
		if err != nil {
			return nil, fmt.Errorf("query_memory: fetch doc %d: %w", f.ID, err)
		}
		results = append(results, QueryResult{Doc: *doc, Score: f.Score})
	}
	return results, nil
}

// UpdateMemory modifies an existing document. Pass empty strings to leave
// fields unchanged. If content changes, the embedding is regenerated.
func (e *Engine) UpdateMemory(id int64, category, topic, content string) (*Doc, error) {
	existing, err := e.getDoc(id)
	if err != nil {
		return nil, fmt.Errorf("update_memory: fetch existing: %w", err)
	}

	if category != "" {
		existing.Category = category
	}
	if topic != "" {
		existing.Topic = topic
	}
	newContent := existing.Content
	if content != "" {
		newContent = content
		existing.Content = content
	}

	tx, err := e.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("update_memory: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(
		`UPDATE docs SET category=?, topic=?, content=? WHERE id=?`,
		existing.Category, existing.Topic, newContent, id,
	); err != nil {
		return nil, fmt.Errorf("update_memory: update doc: %w", err)
	}

	// Regenerate embedding if any field changed (the combined text changes).
	embedding, err := e.embed.Embed(existing.Category + " " + existing.Topic + " " + existing.Content)
	if err != nil {
		return nil, fmt.Errorf("update_memory: generate embedding: %w", err)
	}
	blob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("update_memory: serialize embedding: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM docs_vec WHERE id=?`, id); err != nil {
		return nil, fmt.Errorf("update_memory: delete old vec: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO docs_vec (id, embedding) VALUES (?, ?)`,
		id, blob,
	); err != nil {
		return nil, fmt.Errorf("update_memory: update vec: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("update_memory: commit: %w", err)
	}
	return existing, nil
}

// DeleteMemory removes a document and its vector embedding.
func (e *Engine) DeleteMemory(id int64) error {
	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("delete_memory: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`DELETE FROM docs_vec WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete_memory: delete vec: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM docs WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete_memory: delete doc: %w", err)
	}
	return tx.Commit()
}

// --- internal helpers ---

func (e *Engine) queryFTS(query string, limit int) ([]int64, error) {
	// Escape the query for FTS5: wrap each token and use * for prefix.
	safe := fts5Escape(query)
	rows, err := e.db.Query(
		`SELECT rowid FROM docs_fts WHERE docs_fts MATCH ? ORDER BY rank LIMIT ?`,
		safe, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (e *Engine) queryVec(blob []byte, limit int) ([]int64, error) {
	rows, err := e.db.Query(
		`SELECT id FROM docs_vec WHERE embedding MATCH ? AND k=?
		 ORDER BY distance`,
		blob, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (e *Engine) getDoc(id int64) (*Doc, error) {
	row := e.db.QueryRow(
		`SELECT id, category, topic, content, timestamp FROM docs WHERE id=?`, id,
	)
	var d Doc
	if err := row.Scan(&d.ID, &d.Category, &d.Topic, &d.Content, &d.Timestamp); err != nil {
		return nil, err
	}
	return &d, nil
}

// fts5Escape converts a plain text query to a safe FTS5 MATCH expression.
// Each whitespace-separated token is quoted to avoid injection.
func fts5Escape(query string) string {
	tokens := strings.Fields(query)
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		// Replace double-quotes inside token.
		t = strings.ReplaceAll(t, `"`, `""`)
		quoted = append(quoted, `"`+t+`"`)
	}
	return strings.Join(quoted, " ")
}
