package serve

type CellEnumerator interface {
	ListCells() []Cell
}

type MockEnumerator struct{}

func NewMockEnumerator() *MockEnumerator {
	return &MockEnumerator{}
}

func (m *MockEnumerator) ListCells() []Cell {
	return MockResources()
}

type CompositeEnumerator struct {
	sources []CellEnumerator
}

func NewCompositeEnumerator(sources ...CellEnumerator) *CompositeEnumerator {
	return &CompositeEnumerator{sources: sources}
}

func (c *CompositeEnumerator) ListCells() []Cell {
	var all []Cell
	for _, src := range c.sources {
		all = append(all, src.ListCells()...)
	}
	return all
}
