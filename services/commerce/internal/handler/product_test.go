package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ecommerce/services/commerce/internal/service"
	"github.com/ecommerce/services/commerce/internal/store"
)

var testActor = service.Actor{
	ID:    "e-1",
	Roles: []string{"catalog_admin", "platform_admin"},
	Teams: []string{"commerce"},
}

func newTestServer(t *testing.T) (*http.ServeMux, *store.MemoryStore) {
	t.Helper()
	s := store.NewMemoryStore()
	s.SetClock(nil)
	svc := service.NewProductService(s)
	h := NewProductHandler(svc, nil)

	mux := http.NewServeMux()
	h.Routes(mux)
	return mux, s
}

func request(t *testing.T, mux *http.ServeMux, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r.Header.Set(HeaderActorID, testActor.ID)
	r.Header.Set(HeaderActorRoles, strings.Join(testActor.Roles, ","))
	r.Header.Set(HeaderActorTeams, strings.Join(testActor.Teams, ","))
	r.Header.Set(HeaderActorScopes, "products:read,products:write")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

const validProductJSON = `{
  "id": "p-1",
  "sku": "sku-1",
  "name": "Widget",
  "description": "A widget",
  "price": {"amount_cents": 1999, "currency": "USD"},
  "inventory": {"available": 4, "reserved": 0, "warehouse_id": "wh-1"},
  "owner_team": "commerce",
  "categories": ["tools"],
  "attributes": {"colour": "red"}
}`

func TestHealthAndReady(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)

	for _, path := range []string{"/health", "/ready"} {
		w := request(t, mux, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, w.Code)
		}
	}
}

func TestCreateGetUpdateDeleteFlow(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)

	// Create
	w := request(t, mux, http.MethodPost, "/api/v1/products", validProductJSON)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}

	// Get
	w = request(t, mux, http.MethodGet, "/api/v1/products/p-1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var got productResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Name != "Widget" {
		t.Errorf("Name = %q, want %q", got.Name, "Widget")
	}
	if got.AmountCents != 1999 || got.Currency != "USD" {
		t.Errorf("price = %d %s, want 1999 USD", got.AmountCents, got.Currency)
	}
	if got.Categories == nil || got.Attributes == nil {
		t.Error("categories/attributes are nil in the response; they must marshal as [] and {}")
	}

	// Update
	updated := strings.Replace(validProductJSON, `"name": "Widget"`, `"name": "Gadget"`, 1)
	w = request(t, mux, http.MethodPut, "/api/v1/products/p-1", updated)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	// Delete needs no inventory.
	drained := strings.Replace(validProductJSON, `"available": 4`, `"available": 0`, 1)
	if w = request(t, mux, http.MethodPut, "/api/v1/products/p-1", drained); w.Code != http.StatusOK {
		t.Fatalf("PUT (drain) status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	w = request(t, mux, http.MethodDelete, "/api/v1/products/p-1", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204 (body: %s)", w.Code, w.Body.String())
	}

	w = request(t, mux, http.MethodGet, "/api/v1/products/p-1", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("GET after delete status = %d, want 404", w.Code)
	}
}

func TestListProducts(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	request(t, mux, http.MethodPost, "/api/v1/products", validProductJSON)

	w := request(t, mux, http.MethodGet, "/api/v1/products", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200", w.Code)
	}

	var body struct {
		Products   []productResponse `json:"products"`
		NextPage   string            `json:"next_page_token"`
		TotalCount int               `json:"total_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if len(body.Products) != 1 {
		t.Errorf("products = %d, want 1", len(body.Products))
	}
	if body.TotalCount != 1 {
		t.Errorf("total_count = %d, want 1", body.TotalCount)
	}
}

func TestListRejectsBadPageSize(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	w := request(t, mux, http.MethodGet, "/api/v1/products?page_size=abc", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	w = request(t, mux, http.MethodGet, "/api/v1/products?page_size=-1", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestNotFoundIs404(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	w := request(t, mux, http.MethodGet, "/api/v1/products/nope", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	assertErrorCode(t, w, "NOT_FOUND")
}

func TestInvalidJSONIs400(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	w := request(t, mux, http.MethodPost, "/api/v1/products", "{not json")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestUnknownFieldIs400(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	// A typo'd field must be an error, not a silently ignored value.
	w := request(t, mux, http.MethodPost, "/api/v1/products", `{"id":"p-1","nmae":"typo"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unknown field", w.Code)
	}
}

func TestTrailingGarbageIs400(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	w := request(t, mux, http.MethodPost, "/api/v1/products", validProductJSON+`{"extra":true}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for two JSON objects", w.Code)
	}
}

func TestMissingPriceIs400(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	w := request(t, mux, http.MethodPost, "/api/v1/products", `{"id":"p-1","name":"X"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when price is missing", w.Code)
	}
}

func TestMissingInventoryIs400(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	w := request(t, mux, http.MethodPost, "/api/v1/products",
		`{"id":"p-1","name":"X","price":{"amount_cents":1,"currency":"USD"}}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when inventory is missing", w.Code)
	}
}

func TestUpdatePathIDMismatchIs400(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	request(t, mux, http.MethodPost, "/api/v1/products", validProductJSON)

	other := strings.Replace(validProductJSON, `"id": "p-1"`, `"id": "p-2"`, 1)
	w := request(t, mux, http.MethodPut, "/api/v1/products/p-1", other)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when the body id differs from the path id", w.Code)
	}
}

func TestDuplicateCreateIs409(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	request(t, mux, http.MethodPost, "/api/v1/products", validProductJSON)

	w := request(t, mux, http.MethodPost, "/api/v1/products", validProductJSON)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	assertErrorCode(t, w, "CONFLICT")
}

func TestForbiddenIs403(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	request(t, mux, http.MethodPost, "/api/v1/products", validProductJSON)

	// A viewer with no write permission.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/products", strings.NewReader(validProductJSON))
	r.Header.Set(HeaderActorRoles, "catalog_viewer")
	r.Header.Set(HeaderActorTeams, "commerce")
	r.Header.Set(HeaderActorScopes, "products:read")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	assertErrorCode(t, w, "PERMISSION_DENIED")
}

func TestDeleteWithStockIs409(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	request(t, mux, http.MethodPost, "/api/v1/products", validProductJSON)

	w := request(t, mux, http.MethodDelete, "/api/v1/products/p-1", "")
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 when the product still has stock", w.Code)
	}
}

func TestMethodNotAllowedOnWrongVerb(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	request(t, mux, http.MethodPost, "/api/v1/products", validProductJSON)

	// net/http's method patterns must reject a verb the route does not declare.
	w := request(t, mux, http.MethodDelete, "/api/v1/products", "")
	if w.Code == http.StatusNoContent {
		t.Errorf("DELETE on the collection returned 204, want a rejection")
	}
}

func TestActorFromRequestParsing(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(HeaderActorRoles, " a , b ,, c ")
	r.Header.Set(HeaderActorTeams, "commerce")
	r.Header.Set(HeaderActorScopes, "")

	actor := ActorFromRequest(r)
	if len(actor.Roles) != 3 {
		t.Errorf("Roles = %v, want 3 entries", actor.Roles)
	}
	if actor.Roles[0] != "a" || actor.Roles[2] != "c" {
		t.Errorf("Roles = %v, want [a b c] trimmed", actor.Roles)
	}
	if len(actor.Teams) != 1 {
		t.Errorf("Teams = %v, want [commerce]", actor.Teams)
	}
	if actor.Scopes != nil {
		t.Errorf("Scopes = %v, want nil for an empty header", actor.Scopes)
	}
}

// A request with no identity headers must not inherit any privilege.
func TestActorFromRequestIsFailClosed(t *testing.T) {
	t.Parallel()

	actor := ActorFromRequest(httptest.NewRequest(http.MethodGet, "/", nil))
	if len(actor.Roles) != 0 || len(actor.Teams) != 0 || len(actor.Scopes) != 0 || actor.ID != "" {
		t.Errorf("actor = %+v, want the zero value", actor)
	}
}

func TestOversizedBodyIs400(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)

	huge := `{"id":"p-1","name":"` + strings.Repeat("x", MaxBodyBytes+1024) + `"}`
	w := request(t, mux, http.MethodPost, "/api/v1/products", huge)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body over MaxBodyBytes", w.Code)
	}
}

func TestContentTypeIsJSON(t *testing.T) {
	t.Parallel()

	mux, _ := newTestServer(t)
	w := request(t, mux, http.MethodGet, "/health", "")
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func assertErrorCode(t *testing.T, w *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body: %v (raw: %q)", err, w.Body.String())
	}
	if body.Code != want {
		t.Errorf("error code = %q, want %q", body.Code, want)
	}
}
