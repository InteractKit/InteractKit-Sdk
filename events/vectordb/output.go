package vectordb

type VectorDBQueryResultEvent struct {
	Results []string
}

func (e *VectorDBQueryResultEvent) GetId() string {
	return "vectordb.query_result"
}
