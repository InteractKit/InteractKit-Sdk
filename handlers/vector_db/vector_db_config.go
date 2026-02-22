package vectordb

type VectorDBConfig struct {
	MinConfidence float64 `json:"min_confidence"` // Minimum confidence threshold for considering a vector database query result as relevant. Values range from 0.0 to 1.0, where higher values indicate stricter relevance criteria.
}
