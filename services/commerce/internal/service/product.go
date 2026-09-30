// Package service holds the commerce business logic.
//
// It owns the rules; the handler layer owns transport. Keeping them apart is
// what lets the rules be tested without an HTTP server.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ecommerce/services/commerce/internal/store"
)

// Errors returned by this package. Callers compare with errors.Is and map them
// to transport status codes in the handler layer.
var (
	// ErrNotFound is returned when a product does not exist.
	ErrNotFound = errors.New("service: product not found")
	// ErrInvalidInput is returned when input fails validation.
	ErrInvalidInput = errors.New("service: invalid input")
	// ErrForbidden is returned when the caller may not perform the action.
	ErrForbidden = errors.New("service: forbidden")
	// ErrConflict is returned when the request conflicts with current state.
	ErrConflict = errors.New("service: conflict")
)

// ProductService implements the product catalogue rules on top of a Store.
type ProductService struct {
	store store.Store
	now   func() time.Time
}

// NewProductService creates a service backed by s.
func NewProductService(s store.Store) *ProductService {
	return &ProductService{store: s, now: time.Now}
}

// SetClock overrides the time source. Intended for tests.
func (s *ProductService) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	s.now = now
}

// Actor is the authenticated principal performing an action.
type Actor struct {
	ID     string
	Roles  []string
	Teams  []string
	Scopes []string
}

// HasRole reports whether the actor carries role.
func (a Actor) HasRole(role string) bool {
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasScope reports whether the actor carries scope.
func (a Actor) HasScope(scope string) bool {
	for _, s := range a.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// CanRead reports whether the actor may read the product.
func (a Actor) CanRead(p store.Product) bool {
	// A platform admin is not a member of every product's owning team, so the
	// team check alone would deny them reads of their own platform.
	return a.HasRole("platform_admin") || a.HasScope("products:read") || a.inTeam(p.OwnerTeam)
}

// CanWrite reports whether the actor may write a product in team.
func (a Actor) CanWrite(team string) bool {
	return a.HasRole("platform_admin") || (a.HasRole("catalog_admin") && a.inTeam(team))
}

// CanDelete reports whether the actor may delete products at all.
func (a Actor) CanDelete() bool {
	return a.HasRole("platform_admin")
}

func (a Actor) inTeam(team string) bool {
	if team == "" {
		return false
	}
	for _, t := range a.Teams {
		if t == team {
			return true
		}
	}
	return false
}

// GetProduct returns a single product.
func (s *ProductService) GetProduct(ctx context.Context, actor Actor, id string) (store.Product, error) {
	if strings.TrimSpace(id) == "" {
		return store.Product{}, fmt.Errorf("%w: product id is required", ErrInvalidInput)
	}

	product, err := s.store.Get(ctx, id)
	if err != nil {
		return store.Product{}, translate(err)
	}
	if !actor.CanRead(product) {
		return store.Product{}, fmt.Errorf("%w: not allowed to read product %s", ErrForbidden, id)
	}
	return product, nil
}

// ListProducts returns a filtered page of products.
//
// Filtering happens in the store, so an actor that cannot see a product still
// has it excluded rather than merely hidden by the caller.
func (s *ProductService) ListProducts(ctx context.Context, actor Actor, f store.ListFilter) (store.ListResult, error) {
	result, err := s.store.List(ctx, f)
	if err != nil {
		return store.ListResult{}, translate(err)
	}

	visible := make([]store.Product, 0, len(result.Products))
	for _, p := range result.Products {
		if actor.CanRead(p) {
			visible = append(visible, p)
		}
	}
	result.Products = visible
	return result, nil
}

// CreateProduct stores a new product.
func (s *ProductService) CreateProduct(ctx context.Context, actor Actor, p store.Product) (store.Product, error) {
	if err := validateInput(p); err != nil {
		return store.Product{}, err
	}
	if !actor.CanWrite(p.OwnerTeam) {
		return store.Product{}, fmt.Errorf("%w: not allowed to create products for team %q",
			ErrForbidden, p.OwnerTeam)
	}

	created, err := s.store.Create(ctx, p)
	if err != nil {
		return store.Product{}, translate(err)
	}
	return created, nil
}

// UpdateProduct replaces an existing product.
func (s *ProductService) UpdateProduct(ctx context.Context, actor Actor, p store.Product) (store.Product, error) {
	if err := validateInput(p); err != nil {
		return store.Product{}, err
	}

	existing, err := s.store.Get(ctx, p.ID)
	if err != nil {
		return store.Product{}, translate(err)
	}
	if !actor.CanWrite(p.OwnerTeam) && !actor.CanWrite(existing.OwnerTeam) {
		return store.Product{}, fmt.Errorf("%w: not allowed to update product %s", ErrForbidden, p.ID)
	}

	// A product may not be moved between teams without a platform role: it
	// would otherwise let a team editor rewrite another team's inventory.
	if existing.OwnerTeam != p.OwnerTeam && !actor.HasRole("platform_admin") {
		return store.Product{}, fmt.Errorf("%w: moving a product between teams requires a platform role",
			ErrForbidden)
	}

	updated, err := s.store.Update(ctx, p)
	if err != nil {
		return store.Product{}, translate(err)
	}
	return updated, nil
}

// DeleteProduct removes a product.
func (s *ProductService) DeleteProduct(ctx context.Context, actor Actor, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: product id is required", ErrInvalidInput)
	}
	if !actor.CanDelete() {
		return fmt.Errorf("%w: deleting products requires a platform role", ErrForbidden)
	}
	return translate(s.store.Delete(ctx, id))
}

// DeleteProductIfEmpty removes a product that has no stock in it.
//
// Without this, a catalogue entry can be deleted while units are still reserved
// against it, which strands the reservations.
func (s *ProductService) DeleteProductIfEmpty(ctx context.Context, actor Actor, id string) error {
	product, err := s.store.Get(ctx, id)
	if err != nil {
		return translate(err)
	}
	if product.Available != 0 || product.Reserved != 0 {
		return fmt.Errorf("%w: product %s still has inventory (available=%d reserved=%d)",
			ErrConflict, id, product.Available, product.Reserved)
	}
	return s.DeleteProduct(ctx, actor, id)
}

func validateInput(p store.Product) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if p.PriceAmountCents < 0 {
		return fmt.Errorf("%w: price must not be negative", ErrInvalidInput)
	}
	if strings.TrimSpace(p.PriceCurrency) == "" {
		return fmt.Errorf("%w: price currency is required", ErrInvalidInput)
	}
	if p.Available < 0 || p.Reserved < 0 {
		return fmt.Errorf("%w: inventory must not be negative", ErrInvalidInput)
	}
	if p.Reserved > p.Available {
		return fmt.Errorf("%w: reserved (%d) exceeds available (%d)", ErrInvalidInput, p.Reserved, p.Available)
	}
	return nil
}

// translate maps store-layer sentinels onto service-layer sentinels so that
// callers depend on this package, not on its persistence detail.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, store.ErrDuplicateID):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	case errors.Is(err, store.ErrInvalidProduct):
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return err
	}
}
