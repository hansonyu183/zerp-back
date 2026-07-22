package bob

import (
	"math"
	"testing"
)

func TestValidateCreateNormalizesCodeAndEntityFields(t *testing.T) {
	tests := []struct {
		entity string
		input  CreateDetailInput
	}{
		{EntityCustomer, CreateDetailInput{Code: " cus.01 ", Name: " Customer "}},
		{EntitySupplier, CreateDetailInput{Code: "sup-01", Name: "Supplier"}},
		{EntityEmployee, CreateDetailInput{Code: "emp_01", Name: "Employee"}},
		{EntityProduct, CreateDetailInput{Code: "prd01", Name: "Product", Unit: "piece"}},
		{EntityService, CreateDetailInput{Code: "svc01", Name: "Service", Unit: "hour"}},
		{EntityFundAccount, CreateDetailInput{Code: "cash01", Name: "Cash", Currency: "cny"}},
	}
	for _, test := range tests {
		t.Run(test.entity, func(t *testing.T) {
			data, code, err := validateCreate(test.entity, test.input)
			if err != nil {
				t.Fatalf("validateCreate: %v", err)
			}
			if code == "" || code != normalizeExpectedCode(test.input.Code) {
				t.Fatalf("code = %q", code)
			}
			if data.Name == "" {
				t.Fatal("name was not normalized")
			}
			if test.entity == EntityFundAccount && data.Currency != "CNY" {
				t.Fatalf("currency = %q", data.Currency)
			}
		})
	}
}

func TestValidateDetailRejectsCrossEntityFields(t *testing.T) {
	tests := []struct {
		name   string
		entity string
		data   DetailInput
	}{
		{"customer unit", EntityCustomer, DetailInput{Name: "Customer", Unit: "piece"}},
		{"product missing unit", EntityProduct, DetailInput{Name: "Product"}},
		{"product currency", EntityProduct, DetailInput{Name: "Product", Unit: "piece", Currency: "CNY"}},
		{"fund account missing currency", EntityFundAccount, DetailInput{Name: "Cash"}},
		{"fund account malformed currency", EntityFundAccount, DetailInput{Name: "Cash", Currency: "CN"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateDetail(test.entity, test.data); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestQueryValidationBoundaries(t *testing.T) {
	service := &Service{}
	if _, err := service.Query(t.Context(), EntityCustomer, QueryInput{Page: 1, PageSize: 101}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("page size error = %v", err)
	}
	if _, err := service.Query(t.Context(), EntityCustomer, QueryInput{
		Page: 1, PageSize: 20, Sort: []SortItem{{Field: "createdBy", Order: "asc"}},
	}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("sort error = %v", err)
	}
	if _, err := service.Query(t.Context(), EntityCustomer, QueryInput{Page: math.MaxInt, PageSize: 100}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("offset overflow error = %v", err)
	}
}

func normalizeExpectedCode(value string) string {
	result := make([]byte, 0, len(value))
	start, end := 0, len(value)
	for start < end && value[start] == ' ' {
		start++
	}
	for end > start && value[end-1] == ' ' {
		end--
	}
	for _, b := range []byte(value[start:end]) {
		if b >= 'a' && b <= 'z' {
			b -= 'a' - 'A'
		}
		result = append(result, b)
	}
	return string(result)
}
