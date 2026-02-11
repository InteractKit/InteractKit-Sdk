package vectordb

type VectorDBQueryEvent struct {
	Query string
}

func (e *VectorDBQueryEvent) GetId() string {
	return "vectordb.query"
}
