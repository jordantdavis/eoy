// Package store keeps eoy evidence in a local SQLite file.
//
// Every piece of evidence is one row in the evidence table, with a source that
// says where it came from, for example "slack" or "email". Merged GitHub pull
// requests are evidence with the source "pr" and one more row in
// pull_requests. Notes belong to one piece of evidence, and both evidence and
// notes can have tags. Imports are upserts keyed by (source, external_id), and
// they never remove notes or tags, so annotations survive every re-import.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jordantdavis/eoy/internal/daterange"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// PRSource is the source of pull request evidence. Only a PR sync writes it.
const PRSource = "pr"

// ErrNotFound is returned when an ID matches nothing.
var ErrNotFound = errors.New("not found")

// Store is an open eoy database.
type Store struct {
	db *sql.DB
}

// Counts reports what one import did.
type Counts struct {
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
}

// Evidence is one stored piece of evidence.
type Evidence struct {
	ID         string   `json:"id"`
	Source     string   `json:"source"`
	ExternalID string   `json:"external_id,omitempty"`
	Title      string   `json:"title"`
	Body       string   `json:"body,omitempty"`
	URL        string   `json:"url,omitempty"`
	OccurredAt string   `json:"occurred_at"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
	Tags       []string `json:"tags"`
	NoteCount  int      `json:"note_count"`
	PR         *PRInfo  `json:"pull_request,omitempty"`
	// Notes is set only by Get.
	Notes []Note `json:"notes,omitempty"`
}

// PRInfo holds the fields that only pull request evidence has.
type PRInfo struct {
	GitHubID int64  `json:"github_id"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	Author   string `json:"author"`
}

// NewEvidence is the input for UpsertEvidence.
type NewEvidence struct {
	Source string `json:"source"`
	// ExternalID is the ID of the item in its source. If it is set, a second
	// import of the same source and external ID updates the item.
	ExternalID string   `json:"external_id,omitempty"`
	Title      string   `json:"title"`
	Body       string   `json:"body,omitempty"`
	URL        string   `json:"url,omitempty"`
	OccurredAt string   `json:"occurred_at"`
	Tags       []string `json:"tags,omitempty"`
}

// PR holds the fields that eoy keeps for one merged pull request.
type PR struct {
	GitHubID int64
	Repo     string // owner/name
	Number   int
	Title    string
	URL      string
	Author   string
	Body     string
	MergedAt time.Time
}

// PRID returns the stable evidence ID for a GitHub PR.
func PRID(githubID int64) string {
	return PRSource + ":" + strconv.FormatInt(githubID, 10)
}

// Note is one note on a piece of evidence.
type Note struct {
	ID         int64    `json:"id"`
	EvidenceID string   `json:"evidence_id"`
	Body       string   `json:"body"`
	CreatedAt  string   `json:"created_at"`
	Tags       []string `json:"tags"`
}

// Filter narrows List. Zero values match everything. Several sources match
// any of them. Several tags must all be present.
type Filter struct {
	IDs     []string
	Sources []string
	Range   daterange.Range
	Tags    []string
}

// NoteFilter narrows ListNotes. Zero values match everything.
type NoteFilter struct {
	IDs        []int64
	EvidenceID string
	Tags       []string
}

// Open opens the database at path, creates parent directories, and applies
// any migrations that the file does not have yet.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps the transaction and the pragmas on the same handle.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(names) {
		return fmt.Errorf("the database has schema version %d, but this eoy knows only %d. Run eoy update", version, len(names))
	}
	for i := version; i < len(names); i++ {
		body, err := migrationFS.ReadFile(names[i])
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", names[i], err)
		}
		// PRAGMA does not accept bound parameters.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

var (
	sourcePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	tagPattern    = regexp.MustCompile(`^[a-z0-9]([a-z0-9._/-]*[a-z0-9._/])?$`)
)

// NormalizeSource lowercases and trims a source name and makes sure that it
// is valid.
func NormalizeSource(s string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(s))
	if !sourcePattern.MatchString(n) {
		return "", fmt.Errorf("source %q: use letters, digits, '.', '_' and '-', and start with a letter or digit", s)
	}
	return n, nil
}

// NormalizeTag lowercases and trims a tag and makes sure that it is valid. A
// tag cannot end with '-', because "eoy tag" uses a trailing '-' to remove one.
func NormalizeTag(t string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(t))
	if !tagPattern.MatchString(n) {
		return "", fmt.Errorf("tag %q: use letters, digits, '.', '_', '/' and '-', start with a letter or digit, and do not end with '-'", t)
	}
	return n, nil
}

func normalizeTags(tags []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		n, err := NormalizeTag(t)
		if err != nil {
			return nil, err
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

// evidenceID returns the ID for new evidence. With an external ID, the ID is
// a hash of the source and the external ID, so it is the same on every
// machine. Without one, it is random.
func evidenceID(source, externalID string) string {
	var b []byte
	if externalID != "" {
		sum := sha256.Sum256([]byte(source + "\x00" + externalID))
		b = sum[:8]
	} else {
		b = make([]byte, 8)
		rand.Read(b)
	}
	return source + ":" + hex.EncodeToString(b)
}

const updateEvidence = `
UPDATE evidence SET title = ?, body = ?, url = ?, occurred_at = ?, updated_at = ?
 WHERE id = ? AND (title IS NOT ? OR body IS NOT ? OR url IS NOT ? OR occurred_at IS NOT ?)`

// UpsertEvidence adds evidence in one transaction and returns the ID of each
// item in input order. An item with an external ID that is already stored
// updates that item. Tags are added and never removed.
func (s *Store) UpsertEvidence(ctx context.Context, items []NewEvidence, now time.Time) (Counts, []string, error) {
	var c Counts
	stamp := now.UTC().Format(time.RFC3339)
	ids := make([]string, len(items))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return c, nil, err
	}
	defer tx.Rollback()

	for i, in := range items {
		where := fmt.Sprintf("item %d: ", i+1)
		if in.ExternalID != "" {
			where = fmt.Sprintf("item %d (%s): ", i+1, in.ExternalID)
		}
		source, err := NormalizeSource(in.Source)
		if err != nil {
			return Counts{}, nil, errors.New(where + err.Error())
		}
		if source == PRSource {
			return Counts{}, nil, fmt.Errorf("%sthe source %q is for GitHub PRs. Run eoy sync prs to add them", where, PRSource)
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			return Counts{}, nil, errors.New(where + "the title is empty")
		}
		at := in.OccurredAt
		if at == "" {
			at = stamp
		} else if at, err = daterange.NormalizeTime(at); err != nil {
			return Counts{}, nil, errors.New(where + err.Error())
		}
		tags, err := normalizeTags(in.Tags)
		if err != nil {
			return Counts{}, nil, errors.New(where + err.Error())
		}
		extID := strings.TrimSpace(in.ExternalID)

		var id string
		if extID != "" {
			err := tx.QueryRowContext(ctx, "SELECT id FROM evidence WHERE source = ? AND external_id = ?", source, extID).Scan(&id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return Counts{}, nil, err
			}
		}
		body, url := nullable(in.Body), nullable(strings.TrimSpace(in.URL))
		if id == "" {
			id = evidenceID(source, extID)
			_, err := tx.ExecContext(ctx, `INSERT INTO evidence (id, source, external_id, title, body, url, occurred_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, source, nullable(extID), title, body, url, at, stamp, stamp)
			if err != nil {
				return Counts{}, nil, fmt.Errorf("%s%w", where, err)
			}
			c.Added++
		} else {
			res, err := tx.ExecContext(ctx, updateEvidence, title, body, url, at, stamp, id, title, body, url, at)
			if err != nil {
				return Counts{}, nil, fmt.Errorf("%s%w", where, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				c.Updated++
			} else {
				c.Unchanged++
			}
		}
		if err := addTags(ctx, tx, "evidence_tags", "evidence_id", id, tags); err != nil {
			return Counts{}, nil, err
		}
		ids[i] = id
	}
	return c, ids, tx.Commit()
}

// UpsertPRs adds new PRs and updates changed ones in one transaction.
func (s *Store) UpsertPRs(ctx context.Context, prs []PR, now time.Time) (Counts, error) {
	var c Counts
	stamp := now.UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()

	for _, p := range prs {
		id := PRID(p.GitHubID)
		title, body, url := p.Title, nullable(p.Body), nullable(p.URL)
		at := p.MergedAt.UTC().Format(time.RFC3339)

		var one int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM evidence WHERE id = ?", id).Scan(&one)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Counts{}, err
		}
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO evidence (id, source, external_id, title, body, url, occurred_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, PRSource, strconv.FormatInt(p.GitHubID, 10), title, body, url, at, stamp, stamp); err != nil {
				return Counts{}, fmt.Errorf("write %s: %w", id, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO pull_requests (evidence_id, github_id, repo, number, author) VALUES (?, ?, ?, ?, ?)`,
				id, p.GitHubID, p.Repo, p.Number, p.Author); err != nil {
				return Counts{}, fmt.Errorf("write %s: %w", id, err)
			}
			c.Added++
			continue
		}

		res, err := tx.ExecContext(ctx, updateEvidence, title, body, url, at, stamp, id, title, body, url, at)
		if err != nil {
			return Counts{}, fmt.Errorf("write %s: %w", id, err)
		}
		changed, _ := res.RowsAffected()
		res, err = tx.ExecContext(ctx, `UPDATE pull_requests SET repo = ?, number = ?, author = ?
			WHERE evidence_id = ? AND (repo IS NOT ? OR number IS NOT ? OR author IS NOT ?)`,
			p.Repo, p.Number, p.Author, id, p.Repo, p.Number, p.Author)
		if err != nil {
			return Counts{}, fmt.Errorf("write %s: %w", id, err)
		}
		n, _ := res.RowsAffected()
		if changed+n > 0 {
			c.Updated++
		} else {
			c.Unchanged++
		}
	}
	return c, tx.Commit()
}

const selectEvidence = `
SELECT e.id, e.source, coalesce(e.external_id, ''), e.title, coalesce(e.body, ''), coalesce(e.url, ''),
       e.occurred_at, e.created_at, e.updated_at,
       coalesce((SELECT group_concat(tag, ',') FROM (SELECT tag FROM evidence_tags WHERE evidence_id = e.id ORDER BY tag)), ''),
       (SELECT count(*) FROM notes WHERE evidence_id = e.id),
       p.github_id, p.repo, p.number, p.author
  FROM evidence e LEFT JOIN pull_requests p ON p.evidence_id = e.id
 WHERE 1 = 1`

// List returns evidence, newest first.
func (s *Store) List(ctx context.Context, f Filter) ([]Evidence, error) {
	where, args, err := filterClause(f)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, selectEvidence+where+" ORDER BY e.occurred_at DESC, e.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Evidence{}
	for rows.Next() {
		e, err := scanEvidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEvidence(rows *sql.Rows) (Evidence, error) {
	var e Evidence
	var tags string
	var ghID, number sql.NullInt64
	var repo, author sql.NullString
	err := rows.Scan(&e.ID, &e.Source, &e.ExternalID, &e.Title, &e.Body, &e.URL,
		&e.OccurredAt, &e.CreatedAt, &e.UpdatedAt, &tags, &e.NoteCount, &ghID, &repo, &number, &author)
	if err != nil {
		return e, err
	}
	e.Tags = splitTags(tags)
	if ghID.Valid {
		e.PR = &PRInfo{GitHubID: ghID.Int64, Repo: repo.String, Number: int(number.Int64), Author: author.String}
	}
	return e, nil
}

// filterClause returns " AND ..." conditions for f on the evidence table,
// which the query must name e, and the values that they bind.
func filterClause(f Filter) (string, []any, error) {
	var q string
	var args []any
	if len(f.IDs) > 0 {
		q += " AND e.id IN (" + placeholders(len(f.IDs)) + ")"
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	if len(f.Sources) > 0 {
		q += " AND e.source IN (" + placeholders(len(f.Sources)) + ")"
		for _, src := range f.Sources {
			n, err := NormalizeSource(src)
			if err != nil {
				return "", nil, err
			}
			args = append(args, n)
		}
	}
	if f.Range.From != "" {
		q += " AND substr(e.occurred_at, 1, 10) >= ?"
		args = append(args, f.Range.From)
	}
	if f.Range.To != "" {
		q += " AND substr(e.occurred_at, 1, 10) <= ?"
		args = append(args, f.Range.To)
	}
	for _, t := range f.Tags {
		n, err := NormalizeTag(t)
		if err != nil {
			return "", nil, err
		}
		q += " AND EXISTS (SELECT 1 FROM evidence_tags t WHERE t.evidence_id = e.id AND t.tag = ?)"
		args = append(args, n)
	}
	return q, args, nil
}

// Get returns one piece of evidence with its notes.
func (s *Store) Get(ctx context.Context, id string) (Evidence, error) {
	list, err := s.List(ctx, Filter{IDs: []string{id}})
	if err != nil {
		return Evidence{}, err
	}
	if len(list) == 0 {
		return Evidence{}, evidenceNotFound(id)
	}
	e := list[0]
	e.Notes, err = s.ListNotes(ctx, NoteFilter{EvidenceID: id})
	return e, err
}

// Resolve returns the evidence for each ID in order, without duplicates. An
// unknown ID fails the call.
func (s *Store) Resolve(ctx context.Context, ids []string) ([]Evidence, error) {
	ids = unique(ids)
	list, err := s.List(ctx, Filter{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := map[string]Evidence{}
	for _, e := range list {
		byID[e.ID] = e
	}
	out := make([]Evidence, 0, len(ids))
	for _, id := range ids {
		e, ok := byID[id]
		if !ok {
			return nil, evidenceNotFound(id)
		}
		out = append(out, e)
	}
	return out, nil
}

// DeleteEvidence removes evidence with its notes and tags in one transaction
// and returns how many items it removed. An unknown ID fails the call before
// anything is deleted.
func (s *Store) DeleteEvidence(ctx context.Context, ids []string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	removed := 0
	for _, id := range unique(ids) {
		res, err := tx.ExecContext(ctx, "DELETE FROM evidence WHERE id = ?", id)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, evidenceNotFound(id)
		}
		removed++
	}
	return removed, tx.Commit()
}

// TagEvidence adds and removes tags on one piece of evidence.
func (s *Store) TagEvidence(ctx context.Context, id string, add, remove []string) error {
	return s.changeTags(ctx, "evidence", "evidence_tags", "evidence_id", id, add, remove, evidenceNotFound(id))
}

// TagNote adds and removes tags on one note.
func (s *Store) TagNote(ctx context.Context, id int64, add, remove []string) error {
	return s.changeTags(ctx, "notes", "note_tags", "note_id", id, add, remove, noteNotFound(id))
}

func (s *Store) changeTags(ctx context.Context, table, tagTable, column string, id any, add, remove []string, missing error) error {
	add, err := normalizeTags(add)
	if err != nil {
		return err
	}
	remove, err = normalizeTags(remove)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return missing
	}
	if err != nil {
		return err
	}
	for _, t := range remove {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+tagTable+" WHERE "+column+" = ? AND tag = ?", id, t); err != nil {
			return err
		}
	}
	if err := addTags(ctx, tx, tagTable, column, id, add); err != nil {
		return err
	}
	return tx.Commit()
}

func addTags(ctx context.Context, tx *sql.Tx, tagTable, column string, id any, tags []string) error {
	for _, t := range tags {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO "+tagTable+" ("+column+", tag) VALUES (?, ?)", id, t); err != nil {
			return err
		}
	}
	return nil
}

// AddNote adds a note to a piece of evidence.
func (s *Store) AddNote(ctx context.Context, evidenceID, body string, tags []string, now time.Time) (Note, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Note{}, errors.New("the note is empty")
	}
	tags, err := normalizeTags(tags)
	if err != nil {
		return Note{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, err
	}
	defer tx.Rollback()
	var one int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM evidence WHERE id = ?", evidenceID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, evidenceNotFound(evidenceID)
	}
	if err != nil {
		return Note{}, err
	}
	stamp := now.UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, "INSERT INTO notes (evidence_id, body, created_at) VALUES (?, ?, ?)", evidenceID, body, stamp)
	if err != nil {
		return Note{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Note{}, err
	}
	if err := addTags(ctx, tx, "note_tags", "note_id", id, tags); err != nil {
		return Note{}, err
	}
	if err := tx.Commit(); err != nil {
		return Note{}, err
	}
	sort.Strings(tags)
	return Note{ID: id, EvidenceID: evidenceID, Body: body, CreatedAt: stamp, Tags: tags}, nil
}

// ListNotes returns notes, oldest first.
func (s *Store) ListNotes(ctx context.Context, f NoteFilter) ([]Note, error) {
	q := `SELECT n.id, n.evidence_id, n.body, n.created_at,
	             coalesce((SELECT group_concat(tag, ',') FROM (SELECT tag FROM note_tags WHERE note_id = n.id ORDER BY tag)), '')
	        FROM notes n WHERE 1 = 1`
	var args []any
	if len(f.IDs) > 0 {
		q += " AND n.id IN (" + placeholders(len(f.IDs)) + ")"
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	if f.EvidenceID != "" {
		q += " AND n.evidence_id = ?"
		args = append(args, f.EvidenceID)
	}
	for _, t := range f.Tags {
		n, err := NormalizeTag(t)
		if err != nil {
			return nil, err
		}
		q += " AND EXISTS (SELECT 1 FROM note_tags t WHERE t.note_id = n.id AND t.tag = ?)"
		args = append(args, n)
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY n.created_at, n.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		var n Note
		var tags string
		if err := rows.Scan(&n.ID, &n.EvidenceID, &n.Body, &n.CreatedAt, &tags); err != nil {
			return nil, err
		}
		n.Tags = splitTags(tags)
		out = append(out, n)
	}
	return out, rows.Err()
}

// ResolveNotes returns the note for each ID in order, without duplicates. An
// unknown ID fails the call.
func (s *Store) ResolveNotes(ctx context.Context, ids []int64) ([]Note, error) {
	ids = unique(ids)
	list, err := s.ListNotes(ctx, NoteFilter{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := map[int64]Note{}
	for _, n := range list {
		byID[n.ID] = n
	}
	out := make([]Note, 0, len(ids))
	for _, id := range ids {
		n, ok := byID[id]
		if !ok {
			return nil, noteNotFound(id)
		}
		out = append(out, n)
	}
	return out, nil
}

// DeleteNotes removes notes and their tags in one transaction. An unknown ID
// fails the call before anything is deleted.
func (s *Store) DeleteNotes(ctx context.Context, ids []int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	removed := 0
	for _, id := range unique(ids) {
		res, err := tx.ExecContext(ctx, "DELETE FROM notes WHERE id = ?", id)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, noteNotFound(id)
		}
		removed++
	}
	return removed, tx.Commit()
}

// TagCount is the number of evidence items and notes that have one tag.
type TagCount struct {
	Tag      string `json:"tag"`
	Evidence int    `json:"evidence"`
	Notes    int    `json:"notes"`
}

// Tags returns every tag in use, sorted by name.
func (s *Store) Tags(ctx context.Context) ([]TagCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tag, sum(kind = 'e'), sum(kind = 'n') FROM (
		  SELECT tag, 'e' AS kind FROM evidence_tags
		  UNION ALL
		  SELECT tag, 'n' FROM note_tags)
		 GROUP BY tag ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var c TagCount
		if err := rows.Scan(&c.Tag, &c.Evidence, &c.Notes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func evidenceNotFound(id string) error {
	return fmt.Errorf("evidence %q %w. Run eoy get evidence to see IDs", id, ErrNotFound)
}

func noteNotFound(id int64) error {
	return fmt.Errorf("note %d %w. Run eoy get notes to see IDs", id, ErrNotFound)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func splitTags(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

func unique[T comparable](in []T) []T {
	seen := map[T]bool{}
	out := make([]T, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
