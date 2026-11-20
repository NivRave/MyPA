package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Pinned embeddings (V4-03). The stored memory vectors belong to the embedding model that made
// them; vectors from two models cannot be compared. The embedding_space row (migration 9) records
// that model and the vector size. The app refuses to start when EMBEDDING_MODEL names another
// model (CheckEmbeddingSpace), and Reembed is the one way to move to another model: it replaces
// every vector and the row in one transaction, so the data is in one space before and after,
// never in two. docs/EMBEDDINGS.md is the procedure.

// ErrEmbeddingSpaceMismatch is returned by CheckEmbeddingSpace when the stored vectors were made
// by another model, or have another size, than the one configured.
var ErrEmbeddingSpaceMismatch = errors.New("the stored memory vectors were made by another embedding model")

// embeddedTable is the table whose embeddings are searched. The legacy `memories`
// table is not: nothing reads its vectors (P2-4), and V4-07 drops it.
const embeddedTable = "topic_memories"

// zeroVectorSQL is true for a vector of zeros, the "no embedding yet" a memory gets when its
// embedding could not be computed (models.TopicMemory.BeforeCreate).
const zeroVectorSQL = "vector_norm(embedding) = 0"

// EmbeddingStatus is what `mypa embeddings status` reports.
type EmbeddingStatus struct {
	// Recorded is the model and size the stored vectors were made with; nil when none is recorded.
	Recorded *models.EmbeddingSpace
	// ColumnDimensions is the size the schema stores (vector(N) on topic_memories.embedding).
	ColumnDimensions int
	// Memories is the number of rows in topic_memories; WithoutEmbedding is how many of them hold
	// a zero vector (BL-24: saved without an embedding).
	Memories, WithoutEmbedding int64
}

// EmbeddingStatus reads the recorded embedding space and counts the stored vectors.
func (c *Client) EmbeddingStatus(ctx context.Context) (EmbeddingStatus, error) {
	return embeddingStatus(c.DB.WithContext(ctx))
}

func embeddingStatus(tx *gorm.DB) (EmbeddingStatus, error) {
	var st EmbeddingStatus
	rec, err := recordedSpace(tx)
	if err != nil {
		return st, err
	}
	st.Recorded = rec
	if st.ColumnDimensions, err = embeddingColumnDimensions(tx); err != nil {
		return st, err
	}
	if err := tx.Table(embeddedTable).Count(&st.Memories).Error; err != nil {
		return st, fmt.Errorf("count memories: %w", err)
	}
	if err := tx.Table(embeddedTable).Where(zeroVectorSQL).Count(&st.WithoutEmbedding).Error; err != nil {
		return st, fmt.Errorf("count memories without an embedding: %w", err)
	}
	return st, nil
}

func recordedSpace(tx *gorm.DB) (*models.EmbeddingSpace, error) {
	var rec models.EmbeddingSpace
	err := tx.Where("id = 1").Take(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the recorded embedding model: %w", err)
	}
	return &rec, nil
}

// embeddingColumnDimensions is N in topic_memories.embedding vector(N): for pgvector's type the
// column's type modifier is the size.
func embeddingColumnDimensions(tx *gorm.DB) (int, error) {
	var dims int
	err := tx.Raw(`SELECT atttypmod FROM pg_attribute
		WHERE attrelid = 'public.topic_memories'::regclass AND attname = 'embedding' AND NOT attisdropped`).Scan(&dims).Error
	if err != nil {
		return 0, fmt.Errorf("read the size of the stored vectors: %w", err)
	}
	if dims <= 0 {
		return 0, errors.New("topic_memories.embedding has no fixed size")
	}
	return dims, nil
}

func recordSpace(tx *gorm.DB, model string, dims int) error {
	rec := models.EmbeddingSpace{ID: 1, Model: model, Dimensions: dims, UpdatedAt: time.Now()}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"model", "dimensions", "updated_at"}),
	}).Create(&rec).Error
}

// CheckEmbeddingSpace is run before the app takes any message. It returns nil when the stored
// vectors were made by model at the size the schema stores. A database with no embedded memory
// yet has nothing to compare, so it takes model as its own. Otherwise it returns an error
// wrapping ErrEmbeddingSpaceMismatch that tells the operator what to do. It never changes a
// stored vector.
func (c *Client) CheckEmbeddingSpace(ctx context.Context, model string) error {
	return c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("LOCK TABLE embedding_space IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
			return fmt.Errorf("lock the embedding space: %w", err)
		}
		rec, err := recordedSpace(tx)
		if err != nil {
			return err
		}
		dims, err := embeddingColumnDimensions(tx)
		if err != nil {
			return err
		}
		if rec != nil && rec.Model == model && rec.Dimensions == dims {
			return nil
		}
		var embedded int64
		if err := tx.Table(embeddedTable).Where("NOT (" + zeroVectorSQL + ")").Count(&embedded).Error; err != nil {
			return fmt.Errorf("count embedded memories: %w", err)
		}
		if embedded == 0 {
			slog.Info("no memory is embedded yet: recording the configured embedding model", "model", model, "dimensions", dims)
			return recordSpace(tx, model, dims)
		}
		if rec == nil {
			return fmt.Errorf("%w: %d memories have embeddings but no model is recorded for them; run `mypa embeddings reembed` with the app stopped (docs/EMBEDDINGS.md)",
				ErrEmbeddingSpaceMismatch, embedded)
		}
		return fmt.Errorf("%w: %d memories were embedded with %s (%d dimensions); EMBEDDING_MODEL is %s and the schema stores %d. "+
			"Set EMBEDDING_MODEL back to %s, or re-embed them with the app stopped: `mypa embeddings reembed` (docs/EMBEDDINGS.md)",
			ErrEmbeddingSpaceMismatch, embedded, rec.Model, rec.Dimensions, model, dims, rec.Model)
	})
}

// EmbedBatchFunc returns the embeddings of texts, in order; llm.Embedder.EmbedBatch is one.
type EmbedBatchFunc func(ctx context.Context, texts []string) ([][]float32, error)

// ReembedOptions configure Reembed.
type ReembedOptions struct {
	// Model is the embedding model embed uses; it becomes the recorded one.
	Model string
	// MissingOnly embeds only the memories that have no embedding (a zero vector, BL-24) and
	// leaves the others alone. It needs Model to be the recorded model.
	MissingOnly bool
	// BatchSize is how many texts go to embed at once (default 50).
	BatchSize int
	// Progress, if set, is called after every batch with the rows done and the rows to do.
	Progress func(done, total int)
}

// ReembedResult reports what Reembed did.
type ReembedResult struct {
	// Previous is the model that was recorded before; empty when none was.
	Previous string
	// Model and Dimensions are what is recorded now.
	Model      string
	Dimensions int
	// Embedded is the number of memories given a new vector; Empty the number left without one
	// because their text is empty.
	Embedded, Empty int
}

type reembedRow struct {
	ID   uint
	Fact string
}

// Reembed recomputes the embeddings of the stored memories with embed and records opts.Model as
// the model they were made with. Everything happens in one transaction that holds a lock against
// other writes to topic_memories: if any batch fails, or embed returns a vector of another size
// than the schema stores, nothing is changed. Run it with the app stopped (`mypa embeddings
// reembed`); a running app would wait on the lock for every memory it saves.
func (c *Client) Reembed(ctx context.Context, embed EmbedBatchFunc, opts ReembedOptions) (ReembedResult, error) {
	var res ReembedResult
	if strings.TrimSpace(opts.Model) == "" {
		return res, errors.New("an embedding model is required")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 50
	}
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Readers (recall) go on; writers wait until the new vectors are committed.
		if err := tx.Exec("LOCK TABLE topic_memories IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
			return fmt.Errorf("lock topic_memories: %w", err)
		}
		if err := tx.Exec("LOCK TABLE embedding_space IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
			return fmt.Errorf("lock the embedding space: %w", err)
		}
		rec, err := recordedSpace(tx)
		if err != nil {
			return err
		}
		if rec != nil {
			res.Previous = rec.Model
		}
		dims, err := embeddingColumnDimensions(tx)
		if err != nil {
			return err
		}
		res.Model, res.Dimensions = opts.Model, dims

		q := tx.Table(embeddedTable).Select("id", "fact").Order("id")
		if opts.MissingOnly {
			if rec == nil || rec.Model != opts.Model || rec.Dimensions != dims {
				return fmt.Errorf("--missing adds vectors in the recorded space, but the recorded model is %s and the configured one is %s: run a full re-embed",
					describeSpace(rec), fmt.Sprintf("%s (%d dimensions)", opts.Model, dims))
			}
			q = q.Where(zeroVectorSQL)
		}
		var rows []reembedRow
		if err := q.Scan(&rows).Error; err != nil {
			return fmt.Errorf("read memories: %w", err)
		}

		zero := pgvector.NewVector(make([]float32, dims))
		for start := 0; start < len(rows); start += opts.BatchSize {
			batch := rows[start:min(start+opts.BatchSize, len(rows))]
			var texts []string
			var targets []uint
			for _, r := range batch {
				if strings.TrimSpace(r.Fact) == "" {
					// Nothing to embed. A vector from the old model would be wrong here, so the
					// row gets "no embedding" like any memory whose embedding failed.
					if err := tx.Exec("UPDATE topic_memories SET embedding = ? WHERE id = ?", zero, r.ID).Error; err != nil {
						return fmt.Errorf("clear memory %d: %w", r.ID, err)
					}
					res.Empty++
					continue
				}
				texts = append(texts, r.Fact)
				targets = append(targets, r.ID)
			}
			if len(texts) > 0 {
				vectors, err := embed(ctx, texts)
				if err != nil {
					return fmt.Errorf("embed memories %d to %d: %w", batch[0].ID, batch[len(batch)-1].ID, err)
				}
				if len(vectors) != len(texts) {
					return fmt.Errorf("asked for %d embeddings, got %d", len(texts), len(vectors))
				}
				for i, v := range vectors {
					if len(v) != dims {
						return fmt.Errorf("%s returned a vector of %d numbers; the schema stores %d (a different size needs a schema migration first, docs/EMBEDDINGS.md)", opts.Model, len(v), dims)
					}
					if err := tx.Exec("UPDATE topic_memories SET embedding = ? WHERE id = ?", pgvector.NewVector(v), targets[i]).Error; err != nil {
						return fmt.Errorf("store the embedding of memory %d: %w", targets[i], err)
					}
				}
				res.Embedded += len(vectors)
			}
			if opts.Progress != nil {
				opts.Progress(start+len(batch), len(rows))
			}
		}
		return recordSpace(tx, opts.Model, dims)
	})
	if err != nil {
		return ReembedResult{}, err
	}
	slog.Info("re-embedded memories", "previous_model", res.Previous, "model", res.Model,
		"dimensions", res.Dimensions, "embedded", res.Embedded, "empty", res.Empty, "missing_only", opts.MissingOnly)
	return res, nil
}

func describeSpace(rec *models.EmbeddingSpace) string {
	if rec == nil {
		return "not recorded"
	}
	return fmt.Sprintf("%s (%d dimensions)", rec.Model, rec.Dimensions)
}
