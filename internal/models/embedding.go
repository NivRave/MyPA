package models

import "time"

// EmbeddingDimensions is the size of every stored memory vector (V4-03). The schema fixes it:
// `vector(3072)` columns and HNSW indexes on `halfvec(3072)`. An embedding model that produces
// another size needs a schema migration and a full re-embed; docs/EMBEDDINGS.md lists every place
// the number is written, and TestEmbeddingDimensions_WrittenOnlyWhereTheChecklistSays holds the
// list to the code.
const EmbeddingDimensions = 3072

// EmbeddingSpace records which embedding model made the stored memory vectors (V4-03). It has
// exactly one row. Vectors from two models cannot be compared, so the app refuses to start when
// EMBEDDING_MODEL differs from Model, and only `mypa embeddings reembed` changes it, in the same
// transaction that replaces every vector. The chat model is not part of it: changing the chat
// model or provider leaves this row and the vectors alone.
type EmbeddingSpace struct {
	ID         int       `json:"id" gorm:"primaryKey;autoIncrement:false;check:embedding_space_one_row,id = 1"`
	Model      string    `json:"model" gorm:"not null"`
	Dimensions int       `json:"dimensions" gorm:"not null"`
	UpdatedAt  time.Time `json:"updated_at" gorm:"not null"`
}

// TableName pins the table name.
func (EmbeddingSpace) TableName() string { return "embedding_space" }
