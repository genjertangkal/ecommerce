// Package handler is the HTTP transport layer for the commerce service.
//
// It translates HTTP into service calls and service errors into status codes.
// It contains no business rules: anything that looks like a decision belongs in
// //services/commerce/internal/service.
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ecommerce/platform/observability/logging"
	"github.com/ecommerce/services/commerce/internal/service"
	"github.com/ecommerce/services/commerce/internal/store"
)

// MaxBodyBytes caps request bodies, so an unauthenticated caller cannot make
// the service buffer an arbitrarily large payload.
const MaxBodyBytes = 1 << 20 // 1 MiB

// ProductHandler serves the product catalogue HTTP API.
type ProductHandler struct {
	svc    *service.ProductService
	logger *logging.Logger
}

// NewProductHandler creates a handler.
func NewProductHandler(svc *service.ProductService, logger *logging.Logger) *ProductHandler {
	return &ProductHandler{svc: svc, logger: logger}
}

// Routes registers the catalogue routes on mux.
//
// net/http's method-and-path patterns ("GET /api/v1/products/{id}") require
// Go 1.22 or newer, which the pinned SDK provides.
func (h *ProductHandler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("GET /ready", h.Ready)
	mux.HandleFunc("GET /api/v1/products", h.ListProducts)
	mux.HandleFunc("POST /api/v1/products", h.CreateProduct)
	mux.HandleFunc("GET /api/v1/products/{id}", h.GetProduct)
	mux.HandleFunc("PUT /api/v1/products/{id}", h.UpdateProduct)
	mux.HandleFunc("DELETE /api/v1/products/{id}", h.DeleteProduct)
}

// Health reports process liveness.
func (h *ProductHandler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Ready reports readiness. It is deliberately identical to Health today: the
// in-memory store cannot be "not ready", and a readiness probe that fails for a
// reason the service cannot fix just causes restart loops.
func (h *ProductHandler) Ready(w http.ResponseWriter, r *http.Request) {
	h.Health(w, r)
}

// productRequest is the create/update payload.
type productRequest struct {
	ID          string            `json:"id"`
	SKU         string            `json:"sku"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Price       *priceRequest     `json:"price"`
	Inventory   *inventoryRequest `json:"inventory"`
	OwnerTeam   string            `json:"owner_team"`
	Categories  []string          `json:"categories"`
	Attributes  map[string]string `json:"attributes"`
}

type priceRequest struct {
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

type inventoryRequest struct {
	Available   int32  `json:"available"`
	Reserved    int32  `json:"reserved"`
	WarehouseID string `json:"warehouse_id"`
}

func (r productRequest) toProduct() (store.Product, error) {
	if r.Price == nil {
		return store.Product{}, errors.New("price is required")
	}
	if r.Inventory == nil {
		return store.Product{}, errors.New("inventory is required")
	}
	return store.Product{
		ID:               r.ID,
		SKU:              r.SKU,
		Name:             r.Name,
		Description:      r.Description,
		PriceAmountCents: r.Price.AmountCents,
		PriceCurrency:    r.Price.Currency,
		Available:        r.Inventory.Available,
		Reserved:         r.Inventory.Reserved,
		WarehouseID:      r.Inventory.WarehouseID,
		OwnerTeam:        r.OwnerTeam,
		Categories:       r.Categories,
		Attributes:       r.Attributes,
	}, nil
}

type productResponse struct {
	ID          string            `json:"id"`
	SKU         string            `json:"sku"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	AmountCents int64             `json:"amount_cents"`
	Currency    string            `json:"currency"`
	Available   int32             `json:"available"`
	Reserved    int32             `json:"reserved"`
	WarehouseID string            `json:"warehouse_id"`
	OwnerTeam   string            `json:"owner_team"`
	Categories  []string          `json:"categories"`
	Attributes  map[string]string `json:"attributes"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
}

func newProductResponse(p store.Product) productResponse {
	categories := p.Categories
	if categories == nil {
		categories = []string{}
	}
	attributes := p.Attributes
	if attributes == nil {
		attributes = map[string]string{}
	}
	return productResponse{
		ID:          p.ID,
		SKU:         p.SKU,
		Name:        p.Name,
		Description: p.Description,
		AmountCents: p.PriceAmountCents,
		Currency:    p.PriceCurrency,
		Available:   p.Available,
		Reserved:    p.Reserved,
		WarehouseID: p.WarehouseID,
		OwnerTeam:   p.OwnerTeam,
		Categories:  categories,
		Attributes:  attributes,
		CreatedAt:   p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// GetProduct handles GET /api/v1/products/{id}.
func (h *ProductHandler) GetProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	product, err := h.svc.GetProduct(r.Context(), ActorFromRequest(r), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newProductResponse(product))
}

// ListProducts handles GET /api/v1/products.
func (h *ProductHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	filter := store.ListFilter{
		Category:  r.URL.Query().Get("category"),
		Query:     r.URL.Query().Get("q"),
		PageToken: r.URL.Query().Get("page_token"),
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size < 0 {
			h.writeError(w, r, fmt.Errorf("%w: page_size must be a non-negative integer",
				service.ErrInvalidInput))
			return
		}
		filter.PageSize = size
	}

	result, err := h.svc.ListProducts(r.Context(), ActorFromRequest(r), filter)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	items := make([]productResponse, 0, len(result.Products))
	for _, p := range result.Products {
		items = append(items, newProductResponse(p))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"products":        items,
		"next_page_token": result.NextPageToken,
		"total_count":     result.TotalCount,
	})
}

// CreateProduct handles POST /api/v1/products.
func (h *ProductHandler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	var body productRequest
	if err := decodeJSON(w, r, &body); err != nil {
		h.writeError(w, r, err)
		return
	}
	// The path is authoritative for the ID, so a client cannot smuggle one.
	if id := strings.TrimSpace(body.ID); id == "" {
		h.writeError(w, r, fmt.Errorf("%w: id is required", service.ErrInvalidInput))
		return
	}

	product, err := body.toProduct()
	if err != nil {
		h.writeError(w, r, fmt.Errorf("%w: %v", service.ErrInvalidInput, err))
		return
	}

	created, err := h.svc.CreateProduct(r.Context(), ActorFromRequest(r), product)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, newProductResponse(created))
}

// UpdateProduct handles PUT /api/v1/products/{id}.
func (h *ProductHandler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	var body productRequest
	if err := decodeJSON(w, r, &body); err != nil {
		h.writeError(w, r, err)
		return
	}
	product, err := body.toProduct()
	if err != nil {
		h.writeError(w, r, fmt.Errorf("%w: %v", service.ErrInvalidInput, err))
		return
	}
	// The path is authoritative: a mismatched body ID is a client bug, not a
	// silent overwrite of a different product.
	if product.ID != r.PathValue("id") {
		h.writeError(w, r, fmt.Errorf("%w: body id %q does not match path id %q",
			service.ErrInvalidInput, product.ID, r.PathValue("id")))
		return
	}

	updated, err := h.svc.UpdateProduct(r.Context(), ActorFromRequest(r), product)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newProductResponse(updated))
}

// DeleteProduct handles DELETE /api/v1/products/{id}.
func (h *ProductHandler) DeleteProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.DeleteProductIfEmpty(r.Context(), ActorFromRequest(r), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// errorBody is the error response shape.
type errorBody struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// writeError maps a service error onto an HTTP status code.
func (h *ProductHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := statusFor(err)

	if h.logger != nil {
		fields := map[string]interface{}{"error": err.Error(), "status": status}
		if status >= http.StatusInternalServerError {
			h.logger.Error(r.Context(), "request failed", fields)
		} else {
			h.logger.Warn(r.Context(), "request rejected", fields)
		}
	}

	body := errorBody{Error: http.StatusText(status), Code: code}
	// 5xx messages are for operators, not clients: returning the raw cause can
	// leak internal details, so only client errors echo the message.
	if status < http.StatusInternalServerError {
		body.Detail = err.Error()
	}
	writeJSON(w, status, body)
}

// statusFor maps service sentinels onto HTTP status codes.
func statusFor(err error) (int, string) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, service.ErrInvalidInput):
		return http.StatusBadRequest, "INVALID_ARGUMENT"
	case errors.Is(err, service.ErrForbidden):
		return http.StatusForbidden, "PERMISSION_DENIED"
	case errors.Is(err, service.ErrConflict):
		return http.StatusConflict, "CONFLICT"
	default:
		return http.StatusInternalServerError, "INTERNAL"
	}
}

// decodeJSON reads a size-limited JSON body, rejecting unknown fields so a
// typo'd field name is an error rather than a silently ignored value.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("%w: invalid JSON body: %v", service.ErrInvalidInput, err)
	}

	// A clean io.EOF here means the body held exactly one value. Anything else
	// - including a decode error on trailing data - means the client sent more
	// than one JSON document, which must be rejected rather than ignored.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: body must contain exactly one JSON object", service.ErrInvalidInput)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// A partially written body cannot be reported, so this is best-effort.
	_ = json.NewEncoder(w).Encode(body)
}
