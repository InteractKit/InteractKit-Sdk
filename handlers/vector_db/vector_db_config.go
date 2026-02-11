package vectordb

type VectorDBConfig struct {
	EmbeddingFunction *func(text string) ([]float64, error) // Optional custom embedding function. If not provided, a default embedding function will be used.
	MinConfidence     float64                               // Minimum confidence threshold for considering a vector database query result as relevant. Values range from 0.0 to 1.0, where higher values indicate stricter relevance criteria.
}
