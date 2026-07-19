package gmem

import (
	"context"
	"fmt"
	"strings"
	"time"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
	"github.com/google/uuid"
)

type Client struct {
	cfg    *Config
	db     *falkordb.FalkorDB
	graph  *falkordb.Graph
	Embed  *Embedder
	Schema *Schema
}

func NewClient(cfg *Config) (*Client, error) {
	db, err := falkordb.FalkorDBNew(&falkordb.ConnectionOption{
		Addr:     cfg.FalkorAddr,
		Username: cfg.FalkorUser,
		Password: cfg.FalkorPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("connect falkordb: %w", err)
	}
	c := &Client{
		cfg:   cfg,
		db:    db,
		Embed: NewEmbedder(cfg.EmbedBase, cfg.EmbedKey, cfg.EmbedModel),
	}
	c.graph = db.SelectGraph(cfg.Graph)
	if cfg.SchemaPath != "" {
		s, err := LoadSchema(cfg.SchemaPath)
		if err != nil {
			return nil, err
		}
		c.Schema = s
	} else {
		c.Schema = &Schema{}
	}
	return c, nil
}

// GroupID returns a valid group_id; empty string becomes "default"
func (c *Client) GroupID(g string) string {
	if g == "" {
		return "default"
	}
	return g
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func newUUID() string { return uuid.NewString() }

// normalizeTime parses RFC3339 and normalizes to UTC; empty string returns as-is
func normalizeTime(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return "", fmt.Errorf("invalid RFC3339 time %q: %w", s, err)
	}
	return t.UTC().Format(time.RFC3339), nil
}

// indexQueries aligned with graphiti's FalkorDB indexes (range + fulltext)
var indexQueries = []string{
	"CREATE INDEX FOR (n:Entity) ON (n.uuid, n.group_id, n.name, n.created_at)",
	"CREATE INDEX FOR (n:Episodic) ON (n.uuid, n.group_id, n.created_at, n.valid_at)",
	"CREATE INDEX FOR (n:Community) ON (n.uuid)",
	"CREATE INDEX FOR (n:Saga) ON (n.uuid, n.group_id, n.name)",
	"CREATE INDEX FOR ()-[e:RELATES_TO]-() ON (e.uuid, e.group_id, e.name, e.created_at, e.valid_at, e.invalid_at)",
	"CREATE INDEX FOR ()-[e:MENTIONS]-() ON (e.uuid, e.group_id)",
	"CREATE INDEX FOR ()-[e:HAS_MEMBER]-() ON (e.uuid)",
	"CALL db.idx.fulltext.createNodeIndex('Entity', 'name', 'summary', 'group_id')",
	"CALL db.idx.fulltext.createNodeIndex('Episodic', 'content', 'source', 'source_description', 'group_id')",
	"CALL db.idx.fulltext.createNodeIndex('Community', 'name')",
	"CALL db.idx.fulltext.createRelationshipIndex('RELATES_TO', 'name', 'fact', 'group_id')",
}

// Init creates indexes, idempotent: existing indexes are skipped.
// NOPERM errors are tolerated — the caller may be a restricted user.
func (c *Client) Init() error {
	for _, q := range indexQueries {
		_, err := c.graph.Query(q, nil, nil)
		if err == nil {
			continue
		}
		msg := strings.ToLower(err.Error())
		// tolerate: index already exists, restricted user, or procedure not registered (older FalkorDB)
		if strings.Contains(msg, "already") || strings.Contains(msg, "noperm") || strings.Contains(msg, "not registered") {
			continue
		}
		return fmt.Errorf("init %q: %w", q, err)
	}
	return nil
}

type Status struct {
	FalkorDB  string `json:"falkordb"`
	Graph     string `json:"graph"`
	IndexesOK bool   `json:"indexes_ok"`
	Embedding string `json:"embedding"`
}

func (c *Client) Status() *Status {
	st := &Status{Graph: c.cfg.Graph}
	if _, err := c.db.Conn.Ping(context.Background()).Result(); err != nil {
		st.FalkorDB = "error: " + err.Error()
	} else {
		st.FalkorDB = "ok"
	}
	if _, err := c.graph.Query("CALL db.indexes()", nil, nil); err == nil {
		st.IndexesOK = true
	}
	if _, err := c.Embed.Embed("ping"); err != nil {
		st.Embedding = "error: " + err.Error()
	} else {
		st.Embedding = "ok"
	}
	return st
}
