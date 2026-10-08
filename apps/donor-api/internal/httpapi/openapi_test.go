package httpapi

import (
	"cmp"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

// The contract is JSON rather than YAML so it can be checked with the
// standard library alone.
const contractPath = "../../api/openapi.json"

type contract struct {
	Paths      map[string]map[string]contractOperation `json:"paths"`
	Components struct {
		Schemas map[string]map[string]any `json:"schemas"`
	} `json:"components"`
	raw any
}

type contractOperation struct {
	Security    *[]map[string][]string `json:"security"`
	RateLimit   string                 `json:"x-rate-limit"`
	Clients     []string               `json:"x-clients"`
	RequestBody struct {
		Content map[string]struct {
			Schema map[string]any `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema map[string]any `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

func loadContract(t *testing.T) *contract {
	t.Helper()
	data, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var c contract
	if err = json.Unmarshal(data, &c); err != nil {
		t.Fatalf("%s: %v", contractPath, err)
	}
	if err = json.Unmarshal(data, &c.raw); err != nil {
		t.Fatal(err)
	}
	return &c
}

// operations returns the documented operations keyed like ServeMux patterns.
func (c *contract) operations() map[string]contractOperation {
	ops := map[string]contractOperation{}
	for path, item := range c.Paths {
		for method, op := range item {
			ops[strings.ToUpper(method)+" "+path] = op
		}
	}
	return ops
}

func TestContractListsExactlyTheServedRoutes(t *testing.T) {
	documented := slices.Sorted(maps.Keys(loadContract(t).operations()))
	var served []string
	for _, route := range allRoutes() {
		served = append(served, route.pattern)
	}
	slices.Sort(served)
	for _, pattern := range served {
		if _, ok := slices.BinarySearch(documented, pattern); !ok {
			t.Errorf("%s is served but not documented in %s", pattern, contractPath)
		}
	}
	for _, pattern := range documented {
		if _, ok := slices.BinarySearch(served, pattern); !ok {
			t.Errorf("%s is documented but not served", pattern)
		}
	}
}

func TestContractSecurityMatchesRouteAuth(t *testing.T) {
	schemes := map[auth][]string{noAuth: {}, donorAuth: {"bearerAuth", "cookieAuth"}, staffAuth: {"staffCookieAuth"}, stripeAuth: {"stripeSignature"}}
	ops := loadContract(t).operations()
	for _, route := range allRoutes() {
		op, ok := ops[route.pattern]
		if !ok {
			continue
		}
		if op.Security == nil {
			t.Errorf("%s: security is not declared; use [] for anonymous routes", route.pattern)
			continue
		}
		var got []string
		for _, requirement := range *op.Security {
			got = slices.AppendSeq(got, maps.Keys(requirement))
		}
		slices.Sort(got)
		if want := schemes[route.auth]; !slices.Equal(got, want) {
			t.Errorf("%s: documented security %v, want %v for %s auth", route.pattern, got, want, route.auth)
		}
	}
}

// Documented clients must match what the guard admits: only routes listing
// native accept a request from the app, and every mutation names its limit.
func TestContractClientsMatchTheGuard(t *testing.T) {
	server := NewServer("", Services{})
	for pattern, op := range loadContract(t).operations() {
		mutation := !strings.HasPrefix(pattern, "GET ") && pattern != "POST /donor/stripe/webhook"
		if mutation != (op.RateLimit != "") || mutation != (len(op.Clients) > 0) {
			t.Errorf("%s: x-rate-limit %q and x-clients %v must be set exactly on guarded mutations", pattern, op.RateLimit, op.Clients)
		}
		if !mutation {
			continue
		}
		r := routeRequest(pattern, "{}")
		r.Header.Set("X-DAD-Client", "native")
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, r)
		if admitted := response.Code != http.StatusForbidden; admitted != slices.Contains(op.Clients, "native") {
			t.Errorf("%s: app request got %d, but x-clients is %v", pattern, response.Code, op.Clients)
		}
	}
}

func TestContractReferencesResolve(t *testing.T) {
	c := loadContract(t)
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if target, ok := v["$ref"].(string); ok && c.lookup(target) == nil {
				t.Errorf("unresolved $ref %s", target)
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(c.raw)
}

func (c *contract) lookup(pointer string) any {
	node := c.raw
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		object, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = object[part]
	}
	return node
}

// Each JSON body the API decodes or encodes must have exactly the documented
// properties, recursively, so a renamed or added field fails here first.
func TestContractSchemasMatchGoTypes(t *testing.T) {
	responses := map[string]any{
		"GET /donor/public/content":                                  store.PublicContent{},
		"POST /donor/checkouts":                                      payments.CheckoutResult{},
		"GET /donor/me/dashboard":                                    store.DonorDashboard{},
		"GET /donor/me/profile":                                      store.Profile{},
		"POST /donor/me/preferences":                                 store.Profile{},
		"GET /donor/me/sharing":                                      store.Sharing{},
		"POST /donor/me/sharing":                                     store.Sharing{},
		"GET /donor/me/gifts":                                        store.GiftsPage{},
		"GET /donor/me/receipts/{gift_id}":                           store.Receipt{},
		"GET /donor/me/annual-summary/{year}":                        store.AnnualSummary{},
		"GET /donor/me/recurring":                                    store.RecurringDetails{},
		"GET /donor/me/recurring/amount":                             store.AmountOptions{},
		"GET /donor/me/recurring/pause":                              store.PauseOptions{},
		"GET /donor/me/recurring/resume":                             store.ResumeOptions{},
		"GET /donor/me/payment-method":                               store.PaymentMethodStatus{},
		"POST /donor/me/recurring/cancel":                            store.RecurringDetails{},
		"POST /donor/me/recurring/pause":                             store.PauseResult{},
		"POST /donor/me/recurring/resume":                            store.Resumption{},
		"POST /donor/me/recurring/amount":                            store.AmountChange{},
		"POST /donor/me/payment-method/setup":                        store.PaymentUpdate{},
		"POST /donor/staff/sign-in/options":                          store.Ceremony{},
		"POST /donor/staff/activate/options":                         store.Invitation{},
		"GET /donor/staff/me":                                        store.StaffView{},
		"GET /donor/staff/accounts":                                  []store.StaffView{},
		"POST /donor/staff/accounts/{staff_id}/disable":              store.AccessChange{},
		"POST /donor/staff/accounts/{staff_id}/role":                 store.StaffView{},
		"GET /donor/staff/donors":                                    store.DonorSearch{},
		"GET /donor/staff/donors/{donor_id}":                         store.DonorDetail{},
		"GET /donor/staff/donors/{donor_id}/messages":                store.MessagePage{},
		"GET /donor/staff/donors/{donor_id}/actions":                 store.ActionPage{},
		"GET /donor/staff/donors/{donor_id}/actions/pending":         store.PendingAction{},
		"GET /donor/staff/audit":                                     store.AuditPage{},
		"GET /donor/staff/enrolments":                                store.Enrolments{},
		"POST /donor/staff/messages/{message_id}/resend":             store.QueuedMessage{},
		"GET /donor/staff/donors/{donor_id}/gifts/{gift_id}/refund":  store.RefundQuote{},
		"POST /donor/staff/donors/{donor_id}/gifts/{gift_id}/refund": store.StaffRefund{},
		"GET /donor/staff/donors/{donor_id}/cancellation":            store.RecurringDetails{},
		"POST /donor/staff/donors/{donor_id}/cancellation":           store.StaffCancellation{},
		"GET /donor/staff/exports":                                   store.ExportDatasets{},
		"GET /donor/staff/content":                                   store.ContentInventory{},
		"GET /donor/staff/partners":                                  store.PartnerPage{},
		"POST /donor/staff/content/sources":                          store.StoredSource{},
		"POST /donor/staff/content/publish":                          store.Publication{},
	}
	requests := map[string]any{
		"POST /donor/checkouts":                                      checkoutBody{},
		"POST /donor/public/partner-signup":                          store.PartnerRequest{},
		"POST /donor/me/recurring/cancel":                            cancelBody{},
		"POST /donor/me/recurring/pause":                             pauseBody{},
		"POST /donor/me/recurring/resume":                            resumeBody{},
		"POST /donor/me/recurring/amount":                            amountBody{},
		"POST /donor/staff/sign-in":                                  passkeyBody{},
		"POST /donor/staff/activate/options":                         invitationTokenBody{},
		"POST /donor/staff/activate":                                 passkeyBody{},
		"POST /donor/staff/accounts":                                 invitationBody{},
		"POST /donor/staff/accounts/{staff_id}/role":                 roleBody{},
		"POST /donor/staff/donors/{donor_id}/gifts/{gift_id}/refund": refundBody{},
		"POST /donor/staff/donors/{donor_id}/cancellation":           staffCancelBody{},
		"POST /donor/staff/content/publish":                          store.PublicationRequest{},
	}
	c := loadContract(t)
	ops := c.operations()
	for pattern, value := range responses {
		var schema map[string]any
		for status, response := range ops[pattern].Responses {
			if strings.HasPrefix(status, "2") {
				schema = response.Content["application/json"].Schema
			}
		}
		c.checkShape(t, pattern+" response", reflect.TypeOf(value), schema)
	}
	for pattern, value := range requests {
		c.checkShape(t, pattern+" request", reflect.TypeOf(value), ops[pattern].RequestBody.Content["application/json"].Schema)
	}
	c.checkShape(t, "NativeSession", reflect.TypeOf(nativeSession{}), c.Components.Schemas["NativeSession"])
	c.checkShape(t, "Problem", reflect.TypeOf(problem{}), c.Components.Schemas["Problem"])
}

// resolve follows $ref and the non-null branch of a nullable anyOf.
func (c *contract) resolve(schema map[string]any) map[string]any {
	for {
		if target, ok := schema["$ref"].(string); ok {
			schema = c.Components.Schemas[strings.TrimPrefix(target, "#/components/schemas/")]
			continue
		}
		alternatives, ok := schema["anyOf"].([]any)
		if !ok {
			return schema
		}
		for _, alternative := range alternatives {
			if branch := alternative.(map[string]any); branch["type"] != "null" {
				schema = branch
			}
		}
	}
}

// opaque types serialize as JSON scalars or free-form values.
func opaque(t reflect.Type) bool {
	if t.PkgPath() == "time" || t.PkgPath() == "github.com/jackc/pgx/v5/pgtype" {
		return true
	}
	return t.Kind() == reflect.Map || t.Kind() == reflect.Interface || (t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8)
}

func (c *contract) checkShape(t *testing.T, where string, typ reflect.Type, schema map[string]any) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if opaque(typ) || (typ.Kind() != reflect.Struct && typ.Kind() != reflect.Slice) {
		return
	}
	if schema == nil {
		t.Errorf("%s: no JSON schema documented for %s", where, typ)
		return
	}
	schema = c.resolve(schema)
	if typ.Kind() == reflect.Slice {
		items, _ := schema["items"].(map[string]any)
		c.checkShape(t, where+"[]", typ.Elem(), items)
		return
	}
	properties, _ := schema["properties"].(map[string]any)
	fields := map[string]reflect.Type{}
	for _, field := range reflect.VisibleFields(typ) {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		// encoding/json promotes the fields of an untagged embedded struct.
		if !field.IsExported() || name == "-" || (field.Anonymous && name == "" && field.Type.Kind() == reflect.Struct) {
			continue
		}
		fields[cmp.Or(name, field.Name)] = field.Type
	}
	got, want := slices.Sorted(maps.Keys(properties)), slices.Sorted(maps.Keys(fields))
	if !slices.Equal(got, want) {
		t.Errorf("%s: documented properties %v, %s encodes %v", where, got, typ, want)
		return
	}
	for name, fieldType := range fields {
		property, _ := properties[name].(map[string]any)
		c.checkShape(t, where+"."+name, fieldType, property)
	}
}
