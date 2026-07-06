package localcache

import (
	"errors"
	"testing"
)

// tRow is a test row implementing Record[*tRow, int].
type tRow struct {
	ID   int
	Name string
	Tags []string // reference type — exercises DeepCopy isolation
}

func (r *tRow) GetID() int      { return r.ID }
func (r *tRow) GetValue() *tRow { return r }
func (r *tRow) DeepCopy() *tRow {
	c := *r
	c.Tags = append([]string(nil), r.Tags...)
	return &c
}

func newStub(rows []*tRow) (Cache[*tRow, int], *[]*tRow) {
	src := &rows
	c := NewCustom[*tRow, int](func() ([]*tRow, error) { return *src, nil })
	return c, src
}

func TestReloadAllAndGet(t *testing.T) {
	c, _ := newStub([]*tRow{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}})
	if err := c.ReloadAll(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c.GetTotal() != 2 {
		t.Fatalf("total=%d want 2", c.GetTotal())
	}
	if c.GetUpdateTs() == 0 {
		t.Fatalf("updateTs not set")
	}
	got, err := c.Get(2)
	if err != nil {
		t.Fatalf("get 2: %v", err)
	}
	if got.Name != "b" {
		t.Fatalf("got %q want b", got.Name)
	}
	if _, err := c.Get(99); !errors.Is(err, ErrNotInCache) {
		t.Fatalf("miss err=%v want ErrNotInCache", err)
	}
}

func TestGetBeforeReloadIsMiss(t *testing.T) {
	c, _ := newStub([]*tRow{{ID: 1}})
	if _, err := c.Get(1); !errors.Is(err, ErrNotInCache) {
		t.Fatalf("pre-reload get err=%v want ErrNotInCache", err)
	}
}

func TestDeepCopyIsolation(t *testing.T) {
	c, _ := newStub([]*tRow{{ID: 1, Name: "a", Tags: []string{"x"}}})
	if err := c.ReloadAll(); err != nil {
		t.Fatal(err)
	}
	got, _ := c.Get(1)
	got.Name = "MUTATED"
	got.Tags[0] = "MUTATED"
	// Second read must be unaffected by mutating the first.
	again, _ := c.Get(1)
	if again.Name != "a" || again.Tags[0] != "x" {
		t.Fatalf("cache mutated via returned copy: name=%q tag=%q", again.Name, again.Tags[0])
	}
}

func TestGetAllFilterCopiesToo(t *testing.T) {
	c, _ := newStub([]*tRow{
		{ID: 1, Name: "keep", Tags: []string{"a"}},
		{ID: 2, Name: "drop"},
		{ID: 3, Name: "keep"},
	})
	if err := c.ReloadAll(); err != nil {
		t.Fatal(err)
	}
	all := c.GetAll(func(r *tRow) bool { return r.Name == "keep" })
	if len(all) != 2 {
		t.Fatalf("filtered=%d want 2", len(all))
	}
	all[0].Tags[0] = "MUTATED"
	fresh := c.GetAll(nil)
	for _, r := range fresh {
		if r.ID == 1 && r.Tags[0] != "a" {
			t.Fatalf("GetAll returned non-copy")
		}
	}
	if len(fresh) != 3 {
		t.Fatalf("GetAll(nil)=%d want 3", len(fresh))
	}
}

func TestGetMapFilter(t *testing.T) {
	c, _ := newStub([]*tRow{{ID: 1, Name: "x"}, {ID: 2, Name: "y"}})
	if err := c.ReloadAll(); err != nil {
		t.Fatal(err)
	}
	m := c.GetMap(func(r *tRow) bool { return r.ID == 1 })
	if len(m) != 1 {
		t.Fatalf("map len=%d want 1", len(m))
	}
	if _, ok := m[1]; !ok {
		t.Fatalf("map missing id 1")
	}
}

func TestReloadReplacesData(t *testing.T) {
	c, src := newStub([]*tRow{{ID: 1, Name: "old"}})
	if err := c.ReloadAll(); err != nil {
		t.Fatal(err)
	}
	*src = []*tRow{{ID: 1, Name: "new"}, {ID: 2, Name: "added"}}
	if err := c.ReloadAll(); err != nil {
		t.Fatal(err)
	}
	if c.GetTotal() != 2 {
		t.Fatalf("total=%d want 2 after reload", c.GetTotal())
	}
	got, _ := c.Get(1)
	if got.Name != "new" {
		t.Fatalf("got %q want new", got.Name)
	}
}

func TestReloadPropagatesError(t *testing.T) {
	boom := errors.New("boom")
	c := NewCustom[*tRow, int](func() ([]*tRow, error) { return nil, boom })
	if err := c.ReloadAll(); !errors.Is(err, boom) {
		t.Fatalf("reload err=%v want boom", err)
	}
}
