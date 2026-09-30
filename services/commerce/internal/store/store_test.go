package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newStore(t *testing.T) *MemoryStore {
	t.Helper()
	s := NewMemoryStore()
	s.SetClock(func() time.Time { return fixedTime })
	return s
}

func product(id string) Product {
	return Product{
		ID:               id,
		SKU:              "sku-" + id,
		Name:             "Product " + id,
		Description:      "description " + id,
		PriceAmountCents: 1000,
		PriceCurrency:    "USD",
		Available:        5,
		WarehouseID:      "wh-1",
		OwnerTeam:        "commerce",
		Categories:       []string{"books"},
		Attributes:       map[string]string{"colour": "red"},
	}
}

func TestCreateAndGet(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, product("p-1"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created.CreatedAt.Equal(fixedTime) || !created.UpdatedAt.Equal(fixedTime) {
		t.Errorf("timestamps = %v/%v, want %v", created.CreatedAt, created.UpdatedAt, fixedTime)
	}

	got, err := s.Get(ctx, "p-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != created.Name {
		t.Errorf("Name = %q, want %q", got.Name, created.Name)
	}
}

func TestGetMissing(t *testing.T) {
	t.Parallel()

	_, err := newStore(t).Get(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestCreateRejectsDuplicateID(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, product("p-1")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := s.Create(ctx, product("p-1")); !errors.Is(err, ErrDuplicateID) {
		t.Errorf("Create() error = %v, want ErrDuplicateID", err)
	}
}

func TestCreateValidation(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	cases := map[string]func(*Product){
		"missing id":           func(p *Product) { p.ID = "" },
		"missing name":         func(p *Product) { p.Name = "" },
		"negative price":       func(p *Product) { p.PriceAmountCents = -1 },
		"missing currency":     func(p *Product) { p.PriceCurrency = "" },
		"negative inventory":   func(p *Product) { p.Available = -1 },
		"reserved > available": func(p *Product) { p.Reserved = 99 },
		"missing owner team":   func(p *Product) { p.OwnerTeam = "" },
	}

	for name, mutate := range cases {
		p := product("p-" + name)
		mutate(&p)
		if _, err := s.Create(ctx, p); !errors.Is(err, ErrInvalidProduct) {
			t.Errorf("Create(%s) error = %v, want ErrInvalidProduct", name, err)
		}
	}
}

func TestUpdatePreservesCreatedAt(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	created, _ := s.Create(ctx, product("p-1"))

	updated := created
	updated.Name = "Renamed"
	got, err := s.Update(ctx, updated)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.Name != "Renamed" {
		t.Errorf("Name = %q, want %q", got.Name, "Renamed")
	}
	if !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreatedAt = %v, want it preserved as %v", got.CreatedAt, created.CreatedAt)
	}
}

func TestUpdateMissing(t *testing.T) {
	t.Parallel()

	_, err := newStore(t).Update(context.Background(), product("nope"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update() error = %v, want ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	_, _ = s.Create(ctx, product("p-1"))
	if err := s.Delete(ctx, "p-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := s.Delete(ctx, "p-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
	if s.Len() != 0 {
		t.Errorf("Len() = %d, want 0", s.Len())
	}
}

func TestListIsSortedByID(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	for _, id := range []string{"p-3", "p-1", "p-2"} {
		if _, err := s.Create(ctx, product(id)); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}

	result, err := s.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if result.TotalCount != 3 {
		t.Fatalf("TotalCount = %d, want 3", result.TotalCount)
	}
	for i, want := range []string{"p-1", "p-2", "p-3"} {
		if result.Products[i].ID != want {
			t.Errorf("Products[%d].ID = %q, want %q", i, result.Products[i].ID, want)
		}
	}
}

func TestListFilters(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	books := product("p-books")
	books.Categories = []string{"books"}
	_, _ = s.Create(ctx, books)

	toys := product("p-toys")
	toys.Categories = []string{"toys"}
	_, _ = s.Create(ctx, toys)

	byCategory, err := s.List(ctx, ListFilter{Category: "TOYS"})
	if err != nil {
		t.Fatalf("List(category) error = %v", err)
	}
	if len(byCategory.Products) != 1 || byCategory.Products[0].ID != "p-toys" {
		t.Errorf("category filter returned %+v, want only p-toys", ids(byCategory))
	}

	byQuery, err := s.List(ctx, ListFilter{Query: "renamed"})
	if err != nil {
		t.Fatalf("List(query) error = %v", err)
	}
	if len(byQuery.Products) != 0 {
		t.Errorf("query for a non-existent name returned %v, want none", ids(byQuery))
	}

	bySKU, err := s.List(ctx, ListFilter{Query: "sku-p-books"})
	if err != nil {
		t.Fatalf("List(sku) error = %v", err)
	}
	if len(bySKU.Products) != 1 || bySKU.Products[0].ID != "p-books" {
		t.Errorf("SKU query returned %v, want only p-books", ids(bySKU))
	}
}

func ids(r ListResult) []string {
	out := make([]string, 0, len(r.Products))
	for _, p := range r.Products {
		out = append(out, p.ID)
	}
	return out
}

func TestListPaging(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	for _, id := range []string{"p-1", "p-2", "p-3", "p-4", "p-5"} {
		_, _ = s.Create(ctx, product(id))
	}

	page, err := s.List(ctx, ListFilter{PageSize: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Products) != 2 {
		t.Fatalf("page has %d products, want 2", len(page.Products))
	}
	if page.NextPageToken == "" {
		t.Fatal("NextPageToken is empty, want a cursor for the remaining products")
	}

	next, err := s.List(ctx, ListFilter{PageSize: 2, PageToken: page.NextPageToken})
	if err != nil {
		t.Fatalf("List(page 2) error = %v", err)
	}
	if len(next.Products) != 2 {
		t.Errorf("page 2 has %d products, want 2", len(next.Products))
	}
	if next.Products[0].ID == page.Products[0].ID {
		t.Error("page 2 repeated page 1's first product")
	}
}

func TestListFinalPageHasNoToken(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Create(ctx, product("p-1"))

	page, err := s.List(ctx, ListFilter{PageSize: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.NextPageToken != "" {
		t.Errorf("NextPageToken = %q, want empty on the last page", page.NextPageToken)
	}
}

func TestListRejectsMalformedPageToken(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	for _, token := range []string{"garbage", "offset:abc", "offset:-1", "offset:"} {
		if _, err := s.List(context.Background(), ListFilter{PageToken: token}); err == nil {
			t.Errorf("List(page_token=%q) = nil error, want an error", token)
		}
	}
}

func TestListBeyondEndReturnsEmptyPage(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Create(ctx, product("p-1"))

	page, err := s.List(ctx, ListFilter{PageToken: "offset:99"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Products) != 0 {
		t.Errorf("page has %d products, want 0 past the end", len(page.Products))
	}
}

// A stored product must not be mutable through the value a caller received.
func TestReturnedProductIsACopy(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Create(ctx, product("p-1"))

	got, _ := s.Get(ctx, "p-1")
	got.Name = "tampered"
	got.Categories[0] = "tampered"
	got.Attributes["colour"] = "blue"

	again, _ := s.Get(ctx, "p-1")
	if again.Name == "tampered" {
		t.Error("Name mutation leaked into the store")
	}
	if again.Categories[0] == "tampered" {
		t.Error("Categories mutation leaked into the store")
	}
	if again.Attributes["colour"] == "blue" {
		t.Error("Attributes mutation leaked into the store")
	}
}

func TestCancelledContext(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.Create(ctx, product("p-1")); !errors.Is(err, context.Canceled) {
		t.Errorf("Create() error = %v, want context.Canceled", err)
	}
	if _, err := s.Get(ctx, "p-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("Get() error = %v, want context.Canceled", err)
	}
	if _, err := s.List(ctx, ListFilter{}); !errors.Is(err, context.Canceled) {
		t.Errorf("List() error = %v, want context.Canceled", err)
	}
}

func TestSetClockIgnoresNil(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	s.SetClock(nil)

	created, err := s.Create(context.Background(), product("p-1"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero: the nil clock replaced the default")
	}
}
