package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ecommerce/services/commerce/internal/store"
)

func newService(t *testing.T) (*ProductService, *store.MemoryStore) {
	t.Helper()
	s := store.NewMemoryStore()
	s.SetClock(nil)
	return NewProductService(s), s
}

func product(id string) store.Product {
	return store.Product{
		ID:               id,
		SKU:              "sku-" + id,
		Name:             "Product " + id,
		PriceAmountCents: 2500,
		PriceCurrency:    "USD",
		Available:        10,
		OwnerTeam:        "commerce",
		Categories:       []string{"books"},
	}
}

// Actors used across the tests.
var (
	admin     = Actor{ID: "a", Roles: []string{"platform_admin"}, Teams: []string{"platform"}}
	editor    = Actor{ID: "e", Roles: []string{"catalog_admin"}, Teams: []string{"commerce"}}
	otherEd   = Actor{ID: "o", Roles: []string{"catalog_admin"}, Teams: []string{"identity"}}
	viewer    = Actor{ID: "v", Roles: []string{"catalog_viewer"}, Teams: []string{"commerce"}}
	anonymous = Actor{}
)

func TestGetProduct(t *testing.T) {
	t.Parallel()

	svc, s := newService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, product("p-1")); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if _, err := svc.GetProduct(ctx, editor, "p-1"); err != nil {
		t.Errorf("GetProduct() error = %v, want nil", err)
	}
}

func TestGetProductRequiresID(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	if _, err := svc.GetProduct(context.Background(), editor, "  "); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("error = %v, want ErrInvalidInput", err)
	}
}

func TestGetProductNotFound(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	_, err := svc.GetProduct(context.Background(), editor, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestGetProductForbiddenOutsideTeam(t *testing.T) {
	t.Parallel()

	svc, s := newService(t)
	ctx := context.Background()
	_, _ = s.Create(ctx, product("p-1"))

	if _, err := svc.GetProduct(ctx, otherEd, "p-1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("error = %v, want ErrForbidden", err)
	}
	if _, err := svc.GetProduct(ctx, admin, "p-1"); err != nil {
		t.Errorf("GetProduct() for a platform admin = %v, want nil", err)
	}
}

func TestListProductsHidesUnreadable(t *testing.T) {
	t.Parallel()

	svc, s := newService(t)
	ctx := context.Background()
	_, _ = s.Create(ctx, product("p-commerce"))

	other := product("p-identity")
	other.OwnerTeam = "identity"
	_, _ = s.Create(ctx, other)

	result, err := svc.ListProducts(ctx, editor, store.ListFilter{})
	if err != nil {
		t.Fatalf("ListProducts() error = %v", err)
	}
	if len(result.Products) != 1 || result.Products[0].ID != "p-commerce" {
		t.Errorf("result = %+v, want only the commerce product", result.Products)
	}
}

func TestCreateProduct(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	created, err := svc.CreateProduct(context.Background(), editor, product("p-1"))
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}
	if created.ID != "p-1" {
		t.Errorf("ID = %q, want %q", created.ID, "p-1")
	}
}

func TestCreateProductForbiddenForOtherTeam(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	_, err := svc.CreateProduct(context.Background(), otherEd, product("p-1"))
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("error = %v, want ErrForbidden", err)
	}
}

func TestCreateProductValidation(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx := context.Background()

	cases := map[string]func(*store.Product){
		"missing id":         func(p *store.Product) { p.ID = "" },
		"missing name":       func(p *store.Product) { p.Name = "" },
		"negative price":     func(p *store.Product) { p.PriceAmountCents = -5 },
		"missing currency":   func(p *store.Product) { p.PriceCurrency = "" },
		"reserved>available": func(p *store.Product) { p.Reserved = 11 },
	}

	for name, mutate := range cases {
		p := product("p-" + name)
		mutate(&p)
		if _, err := svc.CreateProduct(ctx, editor, p); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CreateProduct(%s) error = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestCreateProductConflict(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx := context.Background()

	_, _ = svc.CreateProduct(ctx, editor, product("p-1"))
	_, err := svc.CreateProduct(ctx, editor, product("p-1"))
	if !errors.Is(err, ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}
}

func TestUpdateProduct(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.CreateProduct(ctx, editor, product("p-1"))

	updated := product("p-1")
	updated.Name = "Renamed"
	got, err := svc.UpdateProduct(ctx, editor, updated)
	if err != nil {
		t.Fatalf("UpdateProduct() error = %v", err)
	}
	if got.Name != "Renamed" {
		t.Errorf("Name = %q, want %q", got.Name, "Renamed")
	}
}

func TestUpdateProductNotFound(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	_, err := svc.UpdateProduct(context.Background(), editor, product("nope"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// A team editor must not be able to hand a product to another team, which would
// let them rewrite that team's inventory.
func TestUpdateCannotMoveProductBetweenTeams(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.CreateProduct(ctx, editor, product("p-1"))

	moved := product("p-1")
	moved.OwnerTeam = "identity"

	if _, err := svc.UpdateProduct(ctx, editor, moved); !errors.Is(err, ErrForbidden) {
		t.Errorf("error = %v, want ErrForbidden", err)
	}

	if _, err := svc.UpdateProduct(ctx, admin, moved); err != nil {
		t.Errorf("UpdateProduct() for a platform admin = %v, want nil", err)
	}
}

func TestDeleteProductRequiresPlatformRole(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.CreateProduct(ctx, editor, product("p-1"))

	if err := svc.DeleteProduct(ctx, editor, "p-1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("error = %v, want ErrForbidden", err)
	}
	if err := svc.DeleteProduct(ctx, admin, "p-1"); err != nil {
		t.Errorf("DeleteProduct() for an admin = %v, want nil", err)
	}
}

func TestDeleteProductRequiresID(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	if err := svc.DeleteProduct(context.Background(), admin, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("error = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteProductNotFound(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	if err := svc.DeleteProduct(context.Background(), admin, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// Deleting a product with stock in it would strand the reservations.
func TestDeleteProductIfEmptyRejectsStockedProduct(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.CreateProduct(ctx, editor, product("p-1"))

	if err := svc.DeleteProductIfEmpty(ctx, admin, "p-1"); !errors.Is(err, ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}
}

func TestDeleteProductIfEmptySucceedsWhenEmpty(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx := context.Background()

	empty := product("p-1")
	empty.Available = 0
	_, _ = svc.CreateProduct(ctx, editor, empty)

	if err := svc.DeleteProductIfEmpty(ctx, admin, "p-1"); err != nil {
		t.Errorf("DeleteProductIfEmpty() error = %v, want nil", err)
	}
}

func TestDeleteProductIfEmptyRejectsReservedOnly(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx := context.Background()

	// Available 0 with Reserved 0 is required; this checks that a non-zero
	// reserved count also blocks deletion.
	p := product("p-1")
	p.Available = 2
	p.Reserved = 2
	_, _ = svc.CreateProduct(ctx, editor, p)

	zeroed := p
	zeroed.Available = 0
	zeroed.Reserved = 2
	_, _ = svc.UpdateProduct(ctx, editor, zeroed)

	if err := svc.DeleteProductIfEmpty(ctx, admin, "p-1"); !errors.Is(err, ErrConflict) {
		t.Errorf("error = %v, want ErrConflict: reserved stock must block deletion", err)
	}
}

func TestAnonymousActorCanDoNothing(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.CreateProduct(ctx, editor, product("p-1"))

	if _, err := svc.GetProduct(ctx, anonymous, "p-1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("GetProduct() error = %v, want ErrForbidden for an anonymous actor", err)
	}
	if _, err := svc.CreateProduct(ctx, anonymous, product("p-2")); !errors.Is(err, ErrForbidden) {
		t.Errorf("CreateProduct() error = %v, want ErrForbidden", err)
	}
	if err := svc.DeleteProduct(ctx, anonymous, "p-1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("DeleteProduct() error = %v, want ErrForbidden", err)
	}
}

func TestActorHelpers(t *testing.T) {
	t.Parallel()

	a := Actor{Roles: []string{"r1", "r2"}, Scopes: []string{"s1"}}

	if !a.HasRole("r1") || !a.HasRole("r2") {
		t.Error("HasRole() did not find a present role")
	}
	if a.HasRole("r3") {
		t.Error("HasRole() found a role that is not present")
	}
	if !a.HasScope("s1") || a.HasScope("s2") {
		t.Error("HasScope() is wrong")
	}
}

func TestActorTeamMembership(t *testing.T) {
	t.Parallel()

	a := Actor{Teams: []string{"commerce", "identity"}}
	if !a.inTeam("identity") {
		t.Error("inTeam(identity) = false, want true")
	}
	if a.inTeam("fulfillment") {
		t.Error("inTeam(fulfillment) = true, want false")
	}
	if a.inTeam("") {
		t.Error("inTeam(\"\") = true, want false for an empty team")
	}
}

func TestSetClock(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	svc.SetClock(nil) // must not panic
}

func TestCancelledContextPropagates(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.CreateProduct(ctx, editor, product("p-1")); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}
