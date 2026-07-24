package bob

import (
	"encoding/json"
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
		{EntityCategory, CreateDetailInput{Code: "cat01", Name: "产品分类", TargetEntity: EntityProduct}},
		{EntityDepartment, CreateDetailInput{Code: "dept01", Name: "运营部"}},
		{EntityPosition, CreateDetailInput{Code: "pos01", Name: "主管"}},
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
			if test.entity == EntitySupplier && data.SupplierType != SupplierTypeGeneral {
				t.Fatalf("supplier type = %v", data.SupplierType)
			}
			if test.entity == EntityCustomer && data.CustomerType != CustomerTypeEndUser {
				t.Fatalf("customer type = %v", data.CustomerType)
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
	if err != nil || data.SupplierType != SupplierTypeLogisticsPlatform {
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

func TestCommonAttributesNormalizeAndValidate(t *testing.T) {
	customer, _, err := validateCreate(EntityCustomer, CreateDetailInput{
		Code: " customer-1 ", Name: " 客户 ",
		TaxNumber: " ab-123 ", Email: " SALES@EXAMPLE.COM ",
	})
	if err != nil {
		t.Fatalf("validate customer: %v", err)
	}
	if customer.CustomerType != CustomerTypeEndUser || customer.TaxNumber != "AB-123" ||
		customer.Email != "sales@example.com" {
		t.Fatalf("normalized customer = %+v", customer)
	}

	vehicle, _, err := validateCreate(EntityVehicle, CreateDetailInput{
		Code: "vehicle-1", Name: "车辆", PlateNumber: " 沪a12345 ", VehicleType: " 厢式货车 ",
		PlatformObjectID: "01J00000000000000000000020",
		VIN:              " lsvaa4187n2000001 ",
		LoadCapacityKG:   "018000.5",
	})
	if err != nil {
		t.Fatalf("validate vehicle: %v", err)
	}
	if vehicle.VIN != "LSVAA4187N2000001" || vehicle.LoadCapacityKG != "18000.500" {
		t.Fatalf("normalized vehicle = %+v", vehicle)
	}

	account, _, err := validateCreate(EntityFundAccount, CreateDetailInput{
		Code: "account-1", Name: "基本户", Currency: "cny", AccountNumber: " 6222-0000 0001 ",
	})
	if err != nil {
		t.Fatalf("validate account: %v", err)
	}
	if account.AccountNumber != "622200000001" {
		t.Fatalf("account number = %q", account.AccountNumber)
	}

	invalidCases := []struct {
		name   string
		entity string
		data   CreateDetailInput
	}{
		{"invalid customer type", EntityCustomer, CreateDetailInput{
			Code: "CUSTOMER-2", Name: "客户", CustomerType: stringTestPointer("OTHER"),
		}},
		{"invalid date", EntityEmployee, CreateDetailInput{
			Code: "EMPLOYEE-2", Name: "员工", HireDate: "2025-02-30",
		}},
		{"invalid vin", EntityVehicle, CreateDetailInput{
			Code: "VEHICLE-2", Name: "车辆", PlateNumber: "沪A12346", VehicleType: "货车",
			PlatformObjectID: "01J00000000000000000000020", VIN: "LSVAA4187N200000I",
		}},
		{"invalid load capacity", EntityVehicle, CreateDetailInput{
			Code: "VEHICLE-3", Name: "车辆", PlateNumber: "沪A12347", VehicleType: "货车",
			PlatformObjectID: "01J00000000000000000000020", LoadCapacityKG: "0",
		}},
		{"long short name", EntityCustomer, CreateDetailInput{
			Code: "CUSTOMER-3", Name: "客户", ShortName: strings.Repeat("简", 101),
		}},
		{"long remark", EntityService, CreateDetailInput{
			Code: "SERVICE-2", Name: "服务", Unit: "次", Remark: strings.Repeat("注", 1001),
		}},
	}
	for _, test := range invalidCases {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := validateCreate(test.entity, test.data); !errorIsKind(err, ErrorValidation) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestCommonAttributeSaveOmissionAndExplicitClear(t *testing.T) {
	current := DetailView{
		Name: "客户", CustomerType: CustomerTypeDealer, ShortName: "简称",
		TaxNumber: "TAX001", CategoryID: "01J00000000000000000000020",
	}
	var omitted DetailInput
	if err := json.Unmarshal([]byte(`{"name":"更新客户"}`), &omitted); err != nil {
		t.Fatalf("decode omitted input: %v", err)
	}
	merged := mergeDetailInput(current, omitted)
	if merged.ShortName != "简称" || merged.TaxNumber != "TAX001" || merged.CategoryID == "" {
		t.Fatalf("omitted fields were not preserved: %+v", merged)
	}

	var cleared DetailInput
	if err := json.Unmarshal([]byte(`{"name":"更新客户","shortName":null,"taxNumber":""}`), &cleared); err != nil {
		t.Fatalf("decode clear input: %v", err)
	}
	merged = mergeDetailInput(current, cleared)
	if merged.ShortName != "" || merged.TaxNumber != "" || merged.CategoryID == "" {
		t.Fatalf("explicit clear failed: %+v", merged)
	}
}

func TestCategoryAndQueryFilterValidation(t *testing.T) {
	if _, _, err := validateCreate(EntityCategory, CreateDetailInput{
		Code: "cat-1", Name: "产品分类", TargetEntity: EntityProduct,
	}); err != nil {
		t.Fatalf("category rejected: %v", err)
	}
	if _, _, err := validateCreate(EntityCategory, CreateDetailInput{
		Code: "cat-2", Name: "错误分类", TargetEntity: EntityCategory,
	}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("category self target error = %v", err)
	}
	if _, err := validateQueryFilters(EntityEmployee, QueryFilters{
		DepartmentID: "01J00000000000000000000020",
		PositionID:   "01J00000000000000000000021",
	}); err != nil {
		t.Fatalf("employee filters rejected: %v", err)
	}
	if _, err := validateQueryFilters(EntityProduct, QueryFilters{CustomerType: CustomerTypeDealer}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("cross-entity filter error = %v", err)
	}
	if _, err := validateQueryFilters(EntityCategory, QueryFilters{
		ParentID: "01J00000000000000000000020", RootOnly: true,
	}); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("parent/root conflict error = %v", err)
	}

	var explicitEmpty QueryFilters
	if err := json.Unmarshal([]byte(`{"rootOnly":false}`), &explicitEmpty); err != nil {
		t.Fatalf("decode explicit empty filter: %v", err)
	}
	if _, err := validateQueryFilters(EntityCustomer, explicitEmpty); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("explicit cross-entity filter error = %v", err)
	}
	if err := json.Unmarshal([]byte(`{"unknown":true}`), &explicitEmpty); err == nil {
		t.Fatal("unknown nested filter was accepted")
	}

	var crossEntityClear DetailInput
	if err := json.Unmarshal([]byte(`{"name":"客户","vin":null}`), &crossEntityClear); err != nil {
		t.Fatalf("decode cross-entity clear: %v", err)
	}
	if _, err := validateDetail(EntityCustomer, crossEntityClear); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("cross-entity null field error = %v", err)
	}
}

func stringTestPointer(value string) *string {
	return &value
}
