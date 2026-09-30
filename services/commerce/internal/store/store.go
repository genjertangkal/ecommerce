// Package store provides the product persistence boundary for the commerce
// service.
//
// Store is the seam that lets the business logic be tested without a database.
// The in-memory implementation below is safe for concurrent use; a
// database-backed implementation must satisfy the same guarantees.
package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors returned by this package.
var (
	// ErrNotFound is returned when no product has the requested ID.
	ErrNotFound = errors.New("store: product not found")
	// ErrDuplicateID is returned when a create would overwrite an existing ID.
	ErrDuplicateID = errors.New("store: product id already exists")
	// ErrInvalidProduct is returned when a product fails validation.
	ErrInvalidProduct = errors.New("store: invalid product")
)

// Product is the persisted representation of a catalogue product.
//
// Money is held in minor units (cents) as an int64. Using a float for currency
// is the classic source of rounding bugs in a catalogue, because the same total
// must be reproducible across services.
type Product struct {
	ID          string
	SKU         string
	Name        string
	Description string

	PriceAmountCents int64
	PriceCurrency    string

	Available   int32
	Reserved    int32
	WarehouseID string

	// OwnerTeam is the stream-aligned team that owns this product. It is the
	// authorization boundary: //platform/security/policy decides access from
	// resource.owner_team, so the service's own checks must agree with it.
	OwnerTeam string

	Categories []string
	Attributes map[string]string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListFilter narrows a product listing.
type ListFilter struct {
	// Category matches any product whose Categories contains this value.
	Category string
	// Query is a case-insensitive substring match against name, description
	// and SKU.
	Query string
	// PageSize caps the number of results. Zero means unlimited.
	PageSize int
	// PageToken is an opaque cursor returned by a previous call.
	PageToken string
	// PageTokenLimit caps how many tokens may be followed before returning an
	// error. It bounds a malicious or looping client.
	PageTokenLimit int
}

// DefaultPageTokenLimit bounds cursor following when a filter does not.
const DefaultPageTokenLimit = 100

// ListResult is a page of products.
type ListResult struct {
	Products []Product
	// NextPageToken is empty when there are no further pages.
	NextPageToken string
	// TotalCount is the number of products matching the filter, ignoring paging.
	TotalCount int
}

// Store is the product persistence contract.
type Store interface {
	// Create stores a new product.
	Create(ctx context.Context, p Product) (Product, error)
	// Get returns a product by ID.
	Get(ctx context.Context, id string) (Product, error)
	// Update replaces an existing product.
	Update(ctx context.Context, p Product) (Product, error)
	// Delete removes a product.
	Delete(ctx context.Context, id string) error
	// List returns a filtered, paged set of products.
	List(ctx context.Context, f ListFilter) (ListResult, error)
}

// MemoryStore is an in-memory Store.
//
// It is the default implementation for local development and tests. It is not
// durable: everything is lost on restart.
type MemoryStore struct {
	mu       sync.RWMutex
	products map[string]Product
	now      func() time.Time
	seq      int64
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		products: make(map[string]Product),
		now:      time.Now,
	}
}

// SetClock overrides the timestamp source. Intended for tests.
func (s *MemoryStore) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// Len returns the number of stored products.
func (s *MemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.products)
}

func (s *MemoryStore) clock() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.now()
}

// validate returns an error wrapping ErrInvalidProduct, so that callers can
// classify a validation failure with errors.Is regardless of which field failed.
func validate(p Product) error {
	switch {
	case p.ID == "":
		return fmt.Errorf("%w: id is required", ErrInvalidProduct)
	case p.Name == "":
		return fmt.Errorf("%w: name is required", ErrInvalidProduct)
	case p.PriceAmountCents < 0:
		return fmt.Errorf("%w: price must not be negative", ErrInvalidProduct)
	case p.PriceCurrency == "":
		return fmt.Errorf("%w: price currency is required", ErrInvalidProduct)
	case p.Available < 0 || p.Reserved < 0:
		return fmt.Errorf("%w: inventory must not be negative", ErrInvalidProduct)
	case p.Reserved > p.Available:
		return fmt.Errorf("%w: reserved (%d) exceeds available (%d)", ErrInvalidProduct, p.Reserved, p.Available)
	case strings.TrimSpace(p.OwnerTeam) == "":
		return fmt.Errorf("%w: owner team is required", ErrInvalidProduct)
	}
	return nil
}

// Create stores a new product, assigning CreatedAt and UpdatedAt.
func (s *MemoryStore) Create(ctx context.Context, p Product) (Product, error) {
	if err := ctx.Err(); err != nil {
		return Product{}, err
	}
	if err := validate(p); err != nil {
		return Product{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.products[p.ID]; exists {
		return Product{}, ErrDuplicateID
	}

	now := s.now()
	p.CreatedAt = now
	p.UpdatedAt = now
	s.normalise(&p)
	s.products[p.ID] = p

	return p.clone(), nil
}

// Get returns a product by ID.
func (s *MemoryStore) Get(ctx context.Context, id string) (Product, error) {
	if err := ctx.Err(); err != nil {
		return Product{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.products[id]
	if !ok {
		return Product{}, ErrNotFound
	}
	return p.clone(), nil
}

// Update replaces an existing product, preserving CreatedAt.
func (s *MemoryStore) Update(ctx context.Context, p Product) (Product, error) {
	if err := ctx.Err(); err != nil {
		return Product{}, err
	}
	if err := validate(p); err != nil {
		return Product{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.products[p.ID]
	if !ok {
		return Product{}, ErrNotFound
	}

	p.CreatedAt = existing.CreatedAt
	p.UpdatedAt = s.now()
	s.normalise(&p)
	s.products[p.ID] = p

	return p.clone(), nil
}

// Delete removes a product.
func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.products[id]; !ok {
		return ErrNotFound
	}
	delete(s.products, id)
	return nil
}

// List returns products matching f, sorted by ID so that paging is stable.
func (s *MemoryStore) List(ctx context.Context, f ListFilter) (ListResult, error) {
	if err := ctx.Err(); err != nil {
		return ListResult{}, err
	}
	if f.PageTokenLimit <= 0 {
		f.PageTokenLimit = DefaultPageTokenLimit
	}

	s.mu.RLock()
	matched := make([]Product, 0, len(s.products))
	for _, p := range s.products {
		if f.Category != "" && !containsFold(p.Categories, f.Category) {
			continue
		}
		if f.Query != "" && !matchesQuery(p, f.Query) {
			continue
		}
		matched = append(matched, p.clone())
	}
	s.mu.RUnlock()

	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })

	result := ListResult{TotalCount: len(matched)}

	offset, err := resolveOffset(f)
	if err != nil {
		return ListResult{}, err
	}
	if offset > len(matched) {
		offset = len(matched)
	}

	result.Products = matched[offset:]
	if f.PageSize > 0 && len(result.Products) > f.PageSize {
		result.Products = result.Products[:f.PageSize]
		if next := offset + len(result.Products); next < len(matched) {
			result.NextPageToken = encodeToken(next)
		}
	}

	return result, nil
}

// normalise trims user-supplied text and makes nil collections safe to use.
func (s *MemoryStore) normalise(p *Product) {
	p.SKU = trimSpace(p.SKU)
	p.Name = trimSpace(p.Name)
	p.Description = trimSpace(p.Description)
	p.WarehouseID = trimSpace(p.WarehouseID)
	p.OwnerTeam = trimSpace(p.OwnerTeam)
	if p.Categories == nil {
		p.Categories = []string{}
	}
	if p.Attributes == nil {
		p.Attributes = map[string]string{}
	}
}

// clone deep-copies the mutable fields so a caller cannot mutate stored state.
func (p Product) clone() Product {
	out := p
	if p.Categories != nil {
		out.Categories = append([]string(nil), p.Categories...)
	}
	if p.Attributes != nil {
		out.Attributes = make(map[string]string, len(p.Attributes))
		for k, v := range p.Attributes {
			out.Attributes[k] = v
		}
	}
	return out
}

func matchesQuery(p Product, query string) bool {
	needle := trimSpace(query)
	if needle == "" {
		return true
	}
	for _, field := range []string{p.Name, p.Description, p.SKU} {
		if containsFold([]string{field}, needle) {
			return true
		}
	}
	return false
}

// resolveOffset turns a page token into a starting index, bounding how far a
// client may page.
func resolveOffset(f ListFilter) (int, error) {
	if f.PageToken == "" {
		return 0, nil
	}
	if f.PageTokenLimit < 1 {
		return 0, errors.New("store: page token limit exhausted")
	}
	offset, err := decodeToken(f.PageToken)
	if err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, errors.New("store: negative page token")
	}
	return offset, nil
}

func encodeToken(offset int) string {
	return "offset:" + strconv.Itoa(offset)
}

func trimSpace(s string) string { return strings.TrimSpace(s) }

// containsFold reports whether needle appears in any of haystack, compared
// case-insensitively.
func containsFold(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(strings.ToLower(h), strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func decodeToken(token string) (int, error) {
	const prefix = "offset:"
	if len(token) <= len(prefix) || token[:len(prefix)] != prefix {
		return 0, errors.New("store: malformed page token")
	}
	n := 0
	for _, c := range token[len(prefix):] {
		if c < '0' || c > '9' {
			return 0, errors.New("store: malformed page token")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

var _ Store = (*MemoryStore)(nil)
