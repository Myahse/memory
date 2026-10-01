// Package memory contains the memory model and its SQL access layer.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is satisfied by pgx.Tx and *pgxpool.Pool.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var Types = []string{"photo", "screenshot", "pdf", "document", "receipt", "voice", "note", "link"}

func ValidType(t string) bool {
	for _, x := range Types {
		if x == t {
			return true
		}
	}
	return false
}

// FileTypes are types backed by an uploaded file.
func IsFileType(t string) bool {
	switch t {
	case "photo", "screenshot", "receipt", "pdf", "document", "voice":
		return true
	}
	return false
}

type Memory struct {
	ID              string          `json:"id"`
	UserID          string          `json:"-"`
	Type            string          `json:"type"`
	Status          string          `json:"status"`
	Title           string          `json:"title"`
	Content         string          `json:"content,omitempty"`
	Summary         string          `json:"summary"`
	Category        string          `json:"category"`
	Tags            []string        `json:"tags"`
	FilePath        string          `json:"-"`
	ThumbnailPath   string          `json:"-"`
	SourceURL       string          `json:"source_url,omitempty"`
	MimeType        string          `json:"mime_type,omitempty"`
	FileSize        int64           `json:"file_size"`
	ContentHash     string          `json:"content_hash,omitempty"`
	ClientID        string          `json:"client_id,omitempty"`
	CapturedAt      *time.Time      `json:"captured_at,omitempty"`
	Metadata        json.RawMessage `json:"metadata"`
	UserEdited      []string        `json:"user_edited"`
	ProcessingError string          `json:"processing_error,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`

	// Filled by the API layer.
	HasFile      bool   `json:"has_file"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	FileURL      string `json:"file_url,omitempty"`
}

const columns = `m.id, m.user_id, m.type::text, m.status::text, coalesce(m.title,''), coalesce(m.content,''),
  coalesce(m.summary,''), coalesce(m.category,''), coalesce(m.file_path,''), coalesce(m.thumbnail_path,''),
  coalesce(m.source_url,''), coalesce(m.mime_type,''), m.file_size, coalesce(m.content_hash,''),
  coalesce(m.client_id,''), m.captured_at, m.metadata, m.user_edited, coalesce(m.processing_error,''),
  m.created_at, m.updated_at,
  coalesce((select array_agg(t.name order by t.name) from memory_tags mt join tags t on t.id = mt.tag_id
            where mt.memory_id = m.id), '{}')`

func scan(row pgx.Row) (*Memory, error) {
	var m Memory
	var meta []byte
	err := row.Scan(&m.ID, &m.UserID, &m.Type, &m.Status, &m.Title, &m.Content, &m.Summary, &m.Category,
		&m.FilePath, &m.ThumbnailPath, &m.SourceURL, &m.MimeType, &m.FileSize, &m.ContentHash, &m.ClientID,
		&m.CapturedAt, &meta, &m.UserEdited, &m.ProcessingError, &m.CreatedAt, &m.UpdatedAt, &m.Tags)
	if err != nil {
		return nil, err
	}
	m.Metadata = meta
	if len(m.Metadata) == 0 {
		m.Metadata = json.RawMessage("{}")
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.UserEdited == nil {
		m.UserEdited = []string{}
	}
	m.HasFile = m.FilePath != ""
	return &m, nil
}

func Get(ctx context.Context, q Querier, id string) (*Memory, error) {
	return scan(q.QueryRow(ctx, `select `+columns+` from memories m where m.id = $1`, id))
}

// GetForUser is used by the privileged worker; it always scopes by user.
func GetForUser(ctx context.Context, q Querier, userID, id string) (*Memory, error) {
	return scan(q.QueryRow(ctx, `select `+columns+` from memories m where m.id = $1 and m.user_id = $2`, id, userID))
}

// GetMany returns memories in the order of ids (missing ids are skipped).
func GetMany(ctx context.Context, q Querier, ids []string) ([]*Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `select `+columns+` from memories m where m.id = any($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]*Memory{}
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		byID[m.ID] = m
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*Memory, 0, len(ids))
	for _, id := range ids {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

type ListParams struct {
	Types    []string
	Category string
	Tag      string
	Status   string
	From     *time.Time
	To       *time.Time
	Before   *time.Time // cursor: created_at of the last item of the previous page
	Limit    int
}

func List(ctx context.Context, q Querier, p ListParams) ([]*Memory, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if len(p.Types) > 0 {
		add("m.type::text = any($%d)", p.Types)
	}
	if p.Category != "" {
		add("lower(m.category) = lower($%d)", p.Category)
	}
	if p.Status != "" {
		add("m.status::text = $%d", p.Status)
	}
	if p.Tag != "" {
		add("exists (select 1 from memory_tags mt join tags t on t.id = mt.tag_id where mt.memory_id = m.id and lower(t.name) = lower($%d))", p.Tag)
	}
	if p.From != nil {
		add("coalesce(m.captured_at, m.created_at) >= $%d", *p.From)
	}
	if p.To != nil {
		add("coalesce(m.captured_at, m.created_at) < $%d", *p.To)
	}
	if p.Before != nil {
		add("m.created_at < $%d", *p.Before)
	}
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	sql := `select ` + columns + ` from memories m`
	if len(where) > 0 {
		sql += " where " + strings.Join(where, " and ")
	}
	sql += fmt.Sprintf(" order by m.created_at desc limit %d", limit)
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Memory{}
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type NewMemory struct {
	ID          string
	UserID      string
	Type        string
	Title       string
	Content     string
	SourceURL   string
	MimeType    string
	FileSize    int64
	ContentHash string
	ClientID    string
	CapturedAt  *time.Time
	Metadata    map[string]any
}

func Insert(ctx context.Context, q Querier, n NewMemory) (*Memory, error) {
	meta, _ := json.Marshal(n.Metadata)
	if n.Metadata == nil {
		meta = []byte("{}")
	}
	var id string
	err := q.QueryRow(ctx, `
		insert into memories (id, user_id, type, title, content, source_url, mime_type, file_size,
		                      content_hash, client_id, captured_at, metadata)
		values (coalesce(nullif($1,'')::uuid, gen_random_uuid()), $2, $3::memory_type,
		        nullif($4,''), nullif($5,''), nullif($6,''), nullif($7,''), $8, nullif($9,''), nullif($10,''), $11, $12)
		returning id`,
		n.ID, n.UserID, n.Type, n.Title, n.Content, n.SourceURL, n.MimeType, n.FileSize,
		n.ContentHash, n.ClientID, n.CapturedAt, string(meta)).Scan(&id)
	if err != nil {
		return nil, err
	}
	return Get(ctx, q, id)
}

// FindByClientID supports idempotent offline sync.
func FindByClientID(ctx context.Context, q Querier, userID, clientID string) (*Memory, error) {
	return scan(q.QueryRow(ctx, `select `+columns+` from memories m where m.user_id = $1 and m.client_id = $2`, userID, clientID))
}

// FindDuplicates returns memories with the same content hash.
func FindDuplicates(ctx context.Context, q Querier, userID, hash string) ([]*Memory, error) {
	if hash == "" {
		return nil, nil
	}
	rows, err := q.Query(ctx, `select `+columns+` from memories m where m.user_id = $1 and m.content_hash = $2
		order by m.created_at desc limit 5`, userID, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetTags replaces a memory's tags.
func SetTags(ctx context.Context, q Querier, userID, memoryID string, tags []string, source string) error {
	if _, err := q.Exec(ctx, `delete from memory_tags where memory_id = $1 and user_id = $2`, memoryID, userID); err != nil {
		return err
	}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || len(t) > 64 {
			continue
		}
		var tagID string
		err := q.QueryRow(ctx, `
			with existing as (select id from tags where user_id = $1 and lower(name) = lower($2)),
			ins as (insert into tags (user_id, name) select $1, $2 where not exists (select 1 from existing)
			        on conflict do nothing returning id)
			select id from existing union all select id from ins limit 1`, userID, t).Scan(&tagID)
		if err != nil {
			return fmt.Errorf("upsert tag: %w", err)
		}
		if _, err := q.Exec(ctx, `insert into memory_tags (memory_id, tag_id, user_id, source) values ($1, $2, $3, $4)
			on conflict (memory_id, tag_id) do nothing`, memoryID, tagID, userID, source); err != nil {
			return err
		}
	}
	return nil
}

// DeleteOrphanTags removes tags no longer attached to any memory.
func DeleteOrphanTags(ctx context.Context, q Querier, userID string) error {
	_, err := q.Exec(ctx, `delete from tags t where t.user_id = $1 and not exists
		(select 1 from memory_tags mt where mt.tag_id = t.id)`, userID)
	return err
}

// EnqueueJob must run on the privileged connection: clients cannot create jobs.
// It optionally resets the memory status (e.g. "pending" for a retry).
func EnqueueJob(ctx context.Context, q Querier, userID, memoryID, jobType, status string) error {
	if status != "" {
		if _, err := q.Exec(ctx, `update memories set status = $3::memory_status, processing_error = null
			where id = $1 and user_id = $2`, memoryID, userID, status); err != nil {
			return err
		}
	}
	_, err := q.Exec(ctx, `insert into processing_jobs (memory_id, user_id, job_type)
		select id, user_id, $3 from memories where id = $1 and user_id = $2`, memoryID, userID, jobType)
	return err
}

// Patch holds user edits; nil fields are unchanged.
type Patch struct {
	Title      *string    `json:"title"`
	Summary    *string    `json:"summary"`
	Content    *string    `json:"content"`
	Category   *string    `json:"category"`
	Tags       *[]string  `json:"tags"`
	CapturedAt *time.Time `json:"captured_at"`
	Type       *string    `json:"type"`
}

func Update(ctx context.Context, q Querier, userID, id string, p Patch) error {
	var sets []string
	var args []any
	var edited []string
	set := func(col string, v any, cast string) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d%s", col, len(args), cast))
		edited = append(edited, col)
	}
	if p.Title != nil {
		set("title", strings.TrimSpace(*p.Title), "")
	}
	if p.Summary != nil {
		set("summary", strings.TrimSpace(*p.Summary), "")
	}
	if p.Content != nil {
		set("content", *p.Content, "")
	}
	if p.Category != nil {
		set("category", strings.TrimSpace(*p.Category), "")
	}
	if p.CapturedAt != nil {
		set("captured_at", *p.CapturedAt, "")
	}
	if p.Type != nil {
		set("type", *p.Type, "::memory_type")
	}
	if p.Tags != nil {
		edited = append(edited, "tags")
	}
	if len(edited) > 0 {
		args = append(args, edited)
		sets = append(sets, fmt.Sprintf("user_edited = (select array(select distinct unnest(user_edited || $%d::text[])))", len(args)))
		args = append(args, id)
		tag, err := q.Exec(ctx, fmt.Sprintf("update memories set %s where id = $%d", strings.Join(sets, ", "), len(args)), args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
	}
	if p.Tags != nil {
		if err := SetTags(ctx, q, userID, id, *p.Tags, "user"); err != nil {
			return err
		}
		if err := DeleteOrphanTags(ctx, q, userID); err != nil {
			return err
		}
	}
	return nil
}
