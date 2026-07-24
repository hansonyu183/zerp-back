package bob

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]*$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func validateCreate(entity string, input CreateDetailInput) (DetailInput, string, error) {
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if len(code) < 1 || len(code) > 64 || !codePattern.MatchString(code) {
		return DetailInput{}, "", domainError(ErrorValidation, "invalid code", nil, nil)
	}
	data, err := validateDetail(entity, DetailInput{Name: input.Name, Unit: input.Unit, Currency: input.Currency})
	return data, code, err
}

func validateDetail(entity string, input DetailInput) (DetailInput, error) {
	if !validEntity(entity) {
		return DetailInput{}, domainError(ErrorValidation, "invalid entity", nil, nil)
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Unit = strings.TrimSpace(input.Unit)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if !runeLengthBetween(input.Name, 1, 200) {
		return DetailInput{}, domainError(ErrorValidation, "invalid name", nil, nil)
	}
	switch entity {
	case EntityProduct, EntityService:
		if !runeLengthBetween(input.Unit, 1, 32) || input.Currency != "" {
			return DetailInput{}, domainError(ErrorValidation, "invalid unit or unexpected currency", nil, nil)
		}
	case EntityFundAccount:
		if input.Unit != "" || !currencyPattern.MatchString(input.Currency) {
			return DetailInput{}, domainError(ErrorValidation, "invalid currency or unexpected unit", nil, nil)
		}
	case EntityCustomer, EntitySupplier, EntityEmployee, EntityWarehouse:
		if input.Unit != "" || input.Currency != "" {
			return DetailInput{}, domainError(ErrorValidation, "unexpected entity fields", nil, nil)
		}
	default:
		return DetailInput{}, domainError(ErrorValidation, "invalid entity", nil, nil)
	}
	return input, nil
}

func validWriteInput(entity, objectID, versionID string, revision int64, actorID, requestID string) bool {
	return validEntity(entity) && validID(objectID) && validID(versionID) && revision >= 1 && validActorAndRequest(actorID, requestID)
}

func validActorAndRequest(actorID, requestID string) bool {
	return validID(actorID) && requestID != "" && len(requestID) <= 128
}

func validHistoryInput(entity string, input HistoryInput) bool {
	_, validPage := pageOffset(input.Page, input.PageSize)
	return validEntity(entity) && validID(input.ObjectID) && validPage
}

func pageOffset(page, pageSize int) (int32, bool) {
	if page < 1 || pageSize < 1 || pageSize > 100 {
		return 0, false
	}
	pageIndex := int64(page - 1)
	if pageIndex > int64(1<<31-1)/int64(pageSize) {
		return 0, false
	}
	offset := pageIndex * int64(pageSize)
	return int32(offset), true
}

func mustPageOffset(page, pageSize int) int32 {
	offset, _ := pageOffset(page, pageSize)
	return offset
}

func validEntity(entity string) bool { return slices.Contains(entities[:], entity) }

func validStatus(status string) bool {
	return slices.Contains([]string{StatusDraft, StatusPending, StatusRejected, StatusEffective, StatusInvalid}, status)
}

func validID(id string) bool {
	parsed, err := ulid.ParseStrict(id)
	return err == nil && parsed.String() == id
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func optionalComment(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if utf8.RuneCountInString(trimmed) > 1000 {
		return nil, domainError(ErrorValidation, "comment is too long", nil, nil)
	}
	if trimmed == "" {
		return nil, nil
	}
	return &trimmed, nil
}

func runeLengthBetween(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func requiredComment(value *string) (*string, error) {
	comment, err := optionalComment(value)
	if err != nil {
		return nil, err
	}
	if comment == nil {
		return nil, domainError(ErrorValidation, "rejection comment is required", nil, nil)
	}
	return comment, nil
}
