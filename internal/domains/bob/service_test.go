package bob

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func errorIsKind(err error, kind ErrorKind) bool {
	var target *DomainError
	return errors.As(err, &target) && target.Kind == kind
}

func TestValidateCreateNormalizesCodeAndEntityFields(t *testing.T) {
	const platformObjectID = "01J00000000000000000000020"
	tests := []struct {
		entity string
		input  CreateDetailInput
	}{
		{EntityCustomer, CreateDetailInput{Code: " cus.01 ", Name: " Customer "}},
		{EntitySupplier, CreateDetailInput{Code: "sup-01", Name: "Supplier"}},
		{EntityEmployee, CreateDetailInput{Code: "emp_01", Name: "Employee"}},
		{EntityProduct, CreateDetailInput{Code: "prd01", Name: "Product", Unit: "piece"}},
		{EntityService, CreateDetailInput{Code: "svc01", Name: "Service", Unit: "hour"}},
		{EntityWarehouse, CreateDetailInput{Code: "wh01", Name: "主仓"}},
		{EntityVehicle, CreateDetailInput{
			Code: "veh01", Name: "配送车", PlateNumber: " 沪a12345 ",
			VehicleType: " 厢式货车 ", PlatformObjectID: platformObjectID,
		}},
		{EntityFundAccount, CreateDetailInput{Code: "cash01", Name: "Cash", Currency: "cny"}},
	}
	for _, test := range tests {
		t.Run(test.entity, func(t *testing.T) {
			data, code, err := validateCreate(test.entity, test.input)
			if err != nil {
				t.Fatalf("validateCreate: %v", err)
			}
			if code == "" || code != strings.ToUpper(strings.TrimSpace(test.input.Code)) {
				t.Fatalf("code = %q", code)
			}
			if data.Name == "" {
				t.Fatal("name was not normalized")
			}
			if test.entity == EntityFundAccount && data.Currency != "CNY" {
				t.Fatalf("currency = %q", data.Currency)
			}
			if test.entity == EntitySupplier && (data.SupplierType == nil || *data.SupplierType != SupplierTypeGeneral) {
				t.Fatalf("supplier type = %v", data.SupplierType)
			}
			if test.entity == EntityVehicle &&
				(data.PlateNumber != "沪A12345" || data.VehicleType != "厢式货车") {
				t.Fatalf("vehicle data = %+v", data)
			}
		})
	}
}

func TestValidateSupplierTypeCompatibility(t *testing.T) {
	logisticsPlatform := " logistics_platform "
	data, _, err := validateCreate(EntitySupplier, CreateDetailInput{
		Code: "platform01", Name: "物流平台", SupplierType: &logisticsPlatform,
	})
	if err != nil || data.SupplierType == nil || *data.SupplierType != SupplierTypeLogisticsPlatform {
		t.Fatalf("logistics supplier data=%+v err=%v", data, err)
	}
	if _, err = validateDetail(EntitySupplier, DetailInput{Name: "兼容保存"}); err != nil {
		t.Fatalf("omitted supplier type rejected: %v", err)
	}
	invalid := "OTHER"
	if _, err = validateDetail(EntitySupplier, DetailInput{Name: "错误类型", SupplierType: &invalid}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("invalid supplier type error = %v", err)
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
		{"warehouse unit", EntityWarehouse, DetailInput{Name: "Warehouse", Unit: "piece"}},
		{"warehouse currency", EntityWarehouse, DetailInput{Name: "Warehouse", Currency: "CNY"}},
		{"customer supplier type", EntityCustomer, DetailInput{Name: "Customer", SupplierType: stringTestPointer(SupplierTypeGeneral)}},
		{"supplier vehicle field", EntitySupplier, DetailInput{Name: "Supplier", PlateNumber: "沪A12345"}},
		{"vehicle missing plate", EntityVehicle, DetailInput{
			Name: "Vehicle", VehicleType: "Truck", PlatformObjectID: "01J00000000000000000000020",
		}},
		{"vehicle malformed platform", EntityVehicle, DetailInput{
			Name: "Vehicle", PlateNumber: "沪A12345", VehicleType: "Truck", PlatformObjectID: "bad",
		}},
		{"vehicle currency", EntityVehicle, DetailInput{
			Name: "Vehicle", PlateNumber: "沪A12345", VehicleType: "Truck",
			PlatformObjectID: "01J00000000000000000000020", Currency: "CNY",
		}},
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

func TestValidateDetailCountsUnicodeCharacters(t *testing.T) {
	if _, err := validateDetail(EntityCustomer, DetailInput{Name: strings.Repeat("客", 200)}); err != nil {
		t.Fatalf("200-character name rejected: %v", err)
	}
	if _, err := validateDetail(EntityCustomer, DetailInput{Name: strings.Repeat("客", 201)}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("201-character name error = %v", err)
	}
	if _, err := validateDetail(EntityProduct, DetailInput{
		Name: "产品",
		Unit: strings.Repeat("箱", 32),
	}); err != nil {
		t.Fatalf("32-character unit rejected: %v", err)
	}
	if _, err := validateDetail(EntityProduct, DetailInput{
		Name: "产品",
		Unit: strings.Repeat("箱", 33),
	}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("33-character unit error = %v", err)
	}
	if _, err := validateDetail(EntityVehicle, DetailInput{
		Name: "车辆", PlateNumber: strings.Repeat("车", 32), VehicleType: strings.Repeat("型", 64),
		PlatformObjectID: "01J00000000000000000000020",
	}); err != nil {
		t.Fatalf("vehicle Unicode boundary rejected: %v", err)
	}
	if _, err := validateDetail(EntityVehicle, DetailInput{
		Name: "车辆", PlateNumber: strings.Repeat("车", 33), VehicleType: "货车",
		PlatformObjectID: "01J00000000000000000000020",
	}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("33-character plate error = %v", err)
	}
}

func TestCommentCountsUnicodeCharacters(t *testing.T) {
	accepted := strings.Repeat("改", 1000)
	if comment, err := optionalComment(&accepted); err != nil || comment == nil {
		t.Fatalf("1000-character comment rejected: comment=%v err=%v", comment, err)
	}
	rejected := strings.Repeat("改", 1001)
	if _, err := optionalComment(&rejected); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("1001-character comment error = %v", err)
	}
}

func stringTestPointer(value string) *string {
	return &value
}
