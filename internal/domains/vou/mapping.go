package vou

import (
	"context"
	"time"

	dbsqlc "github.com/hansonyu183/zerp-back/internal/database/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Service) loadData(
	ctx context.Context, q *dbsqlc.Queries, document dbsqlc.VouDocument,
) (DocumentDataView, error) {
	data := DocumentDataView{
		BusinessDate: formatDate(document.BusinessDate), Currency: document.Currency, Remark: deref(document.Remark),
	}
	switch document.Entity {
	case EntitySaleOrder:
		detail, err := q.GetVouSaleOrderDetail(ctx, document.ID)
		if err != nil {
			return data, err
		}
		data.Customer = reference(detail.CustomerObjectID, detail.CustomerVersionID, "customer", detail.CustomerCode, detail.CustomerName, "", "", "")
		setSaleExecutionView(&data, detail.OutboundDate, detail.SignoffDate,
			detail.PlatformObjectID, detail.PlatformVersionID, detail.PlatformCode, detail.PlatformName,
			detail.VehicleObjectID, detail.VehicleVersionID, detail.VehicleCode, detail.VehicleName,
			detail.VehiclePlateNumber, detail.DifferenceReason)
		data.ProductLines, err = loadProductLines(ctx, q, document.ID)
		return data, err
	case EntityPurchaseOrder:
		detail, err := q.GetVouPurchaseOrderDetail(ctx, document.ID)
		if err != nil {
			return data, err
		}
		data.Supplier = reference(detail.SupplierObjectID, detail.SupplierVersionID, "supplier", detail.SupplierCode, detail.SupplierName, "", "", "")
		data.InboundDate = formatDate(detail.InboundDate)
		data.DifferenceReason = deref(detail.DifferenceReason)
		data.ProductLines, err = loadProductLines(ctx, q, document.ID)
		return data, err
	case EntityIntermediarySaleOrder:
		detail, err := q.GetVouIntermediarySaleOrderDetail(ctx, document.ID)
		if err != nil {
			return data, err
		}
		data.Customer = reference(detail.CustomerObjectID, detail.CustomerVersionID, "customer", detail.CustomerCode, detail.CustomerName, "", "", "")
		data.Supplier = reference(detail.SupplierObjectID, detail.SupplierVersionID, "supplier", detail.SupplierCode, detail.SupplierName, "", "", "")
		setSaleExecutionView(&data, detail.OutboundDate, detail.SignoffDate,
			detail.PlatformObjectID, detail.PlatformVersionID, detail.PlatformCode, detail.PlatformName,
			detail.VehicleObjectID, detail.VehicleVersionID, detail.VehicleCode, detail.VehicleName,
			detail.VehiclePlateNumber, detail.DifferenceReason)
		data.ProductLines, err = loadProductLines(ctx, q, document.ID)
		return data, err
	case EntityReceipt:
		detail, err := q.GetVouReceiptDetail(ctx, document.ID)
		if err != nil {
			return data, err
		}
		data.Counterparty = reference(detail.CounterpartyObjectID, detail.CounterpartyVersionID, detail.CounterpartyEntity,
			detail.CounterpartyCode, detail.CounterpartyName, "", "", "")
		data.FundAccount = reference(detail.FundAccountObjectID, detail.FundAccountVersionID, "fund-account",
			detail.FundAccountCode, detail.FundAccountName, "", document.Currency, "")
	case EntityPayment:
		detail, err := q.GetVouPaymentDetail(ctx, document.ID)
		if err != nil {
			return data, err
		}
		data.Counterparty = reference(detail.CounterpartyObjectID, detail.CounterpartyVersionID, detail.CounterpartyEntity,
			detail.CounterpartyCode, detail.CounterpartyName, "", "", "")
		data.FundAccount = reference(detail.FundAccountObjectID, detail.FundAccountVersionID, "fund-account",
			detail.FundAccountCode, detail.FundAccountName, "", document.Currency, "")
	case EntityExpenseReimbursement:
		detail, err := q.GetVouExpenseReimbursementDetail(ctx, document.ID)
		if err != nil {
			return data, err
		}
		data.Employee = reference(detail.EmployeeObjectID, detail.EmployeeVersionID, "employee",
			detail.EmployeeCode, detail.EmployeeName, "", "", "")
		data.FundAccount = reference(detail.FundAccountObjectID, detail.FundAccountVersionID, "fund-account",
			detail.FundAccountCode, detail.FundAccountName, "", document.Currency, "")
		rows, err := q.ListVouExpenseLines(ctx, document.ID)
		if err != nil {
			return data, err
		}
		data.ExpenseLines = make([]ExpenseLineView, 0, len(rows))
		for _, row := range rows {
			data.ExpenseLines = append(data.ExpenseLines, ExpenseLineView{
				LineID: row.ID, LineNo: row.LineNo, Category: row.Category,
				Description: row.Description, Amount: formatMoney(row.AmountCents),
			})
		}
	case EntityOtherIncome:
		detail, err := q.GetVouOtherIncomeDetail(ctx, document.ID)
		if err != nil {
			return data, err
		}
		data.SourceName = detail.SourceName
		if detail.CounterpartyObjectID != nil {
			data.Counterparty = reference(deref(detail.CounterpartyObjectID), deref(detail.CounterpartyVersionID),
				deref(detail.CounterpartyEntity), deref(detail.CounterpartyCode), deref(detail.CounterpartyName), "", "", "")
		}
		data.FundAccount = reference(detail.FundAccountObjectID, detail.FundAccountVersionID, "fund-account",
			detail.FundAccountCode, detail.FundAccountName, "", document.Currency, "")
	}
	return data, nil
}

func loadProductLines(ctx context.Context, q *dbsqlc.Queries, documentID string) ([]ProductLineView, error) {
	rows, err := q.ListVouProductLines(ctx, documentID)
	if err != nil {
		return nil, err
	}
	items := make([]ProductLineView, 0, len(rows))
	for _, row := range rows {
		item := ProductLineView{
			LineID: row.ID, LineNo: row.LineNo,
			Product: *reference(row.ProductObjectID, row.ProductVersionID, "product",
				row.ProductCode, row.ProductName, row.ProductUnit, "", ""),
			OrderedQuantity: formatQuantity(row.OrderedQtyMicros),
			UnitPrice:       formatMoney(row.UnitPriceCents), LineAmount: formatMoney(row.LineAmountCents),
		}
		item.OutboundQuantity = formatOptionalQuantity(row.OutboundQtyMicros)
		item.SignedQuantity = formatOptionalQuantity(row.SignedQtyMicros)
		item.RejectedQuantity = formatOptionalQuantity(row.RejectedQtyMicros)
		item.LossQuantity = formatOptionalQuantity(row.LossQtyMicros)
		item.InboundQuantity = formatOptionalQuantity(row.InboundQtyMicros)
		items = append(items, item)
	}
	return items, nil
}

func setSaleExecutionView(
	data *DocumentDataView,
	outboundDate, signoffDate pgtype.Date,
	platformObjectID, platformVersionID, platformCode, platformName *string,
	vehicleObjectID, vehicleVersionID, vehicleCode, vehicleName, vehiclePlate, differenceReason *string,
) {
	data.OutboundDate = formatDate(outboundDate)
	data.SignoffDate = formatDate(signoffDate)
	if platformObjectID != nil {
		data.Platform = reference(deref(platformObjectID), deref(platformVersionID), "supplier",
			deref(platformCode), deref(platformName), "", "", "")
	}
	if vehicleObjectID != nil {
		data.Vehicle = reference(deref(vehicleObjectID), deref(vehicleVersionID), "vehicle",
			deref(vehicleCode), deref(vehicleName), "", "", deref(vehiclePlate))
	}
	data.DifferenceReason = deref(differenceReason)
}

func reference(objectID, versionID, entity, code, name, unit, currency, plate string) *ReferenceView {
	return &ReferenceView{
		ObjectID: objectID, VersionID: versionID, Entity: entity, Code: code, Name: name,
		Unit: unit, Currency: currency, PlateNumber: plate,
	}
}

func documentView(document dbsqlc.VouDocument, data DocumentDataView, attachments []AttachmentView) DocumentView {
	return DocumentView{
		DocumentID: document.ID, Entity: document.Entity, DocumentNo: document.DocumentNo,
		Status: document.Status, Revision: document.Revision, Amount: formatMoney(document.TotalAmountCents),
		Data: data, Attachments: attachments,
		CreatedAt: document.CreatedAt.Time, CreatedBy: document.CreatedBy,
		UpdatedAt: document.UpdatedAt.Time, UpdatedBy: document.UpdatedBy,
		ReviewedAt: optionalTime(document.ReviewedAt), ReviewedBy: document.ReviewedBy,
		ApprovedAt: optionalTime(document.ApprovedAt), ApprovedBy: document.ApprovedBy,
		ExecutedAt: optionalTime(document.ExecutedAt), ExecutedBy: document.ExecutedBy,
	}
}

func attachmentViews(rows []dbsqlc.ListVouAttachmentsRow) []AttachmentView {
	items := make([]AttachmentView, 0, len(rows))
	for _, row := range rows {
		items = append(items, AttachmentView{
			FileID: row.ID, FileName: row.OriginalName, ContentType: row.ContentType,
			Size: row.DeclaredSize, SHA256: row.Sha256Hex, Status: row.Status,
			StoredAt: optionalTime(row.StoredAt), CreatedAt: row.CreatedAt.Time, CreatedBy: row.CreatedBy,
		})
	}
	return items
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func formatOptionalQuantity(value *int64) string {
	if value == nil {
		return ""
	}
	return formatQuantity(*value)
}
