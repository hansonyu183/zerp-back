//go:build integration

package vou

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/hansonyu183/zerp-back/internal/api/authorization"
	bobdomain "github.com/hansonyu183/zerp-back/internal/domains/bob"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	integrationActorOne = "01J00000000000000000000000"
	integrationActorTwo = "01J00000000000000000000001"
)

type integrationReferences struct {
	customer, supplier, employee, product, fundAccount, platform, vehicle ReferenceInput
}

func vouIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	expectedName := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DB"))
	if databaseURL == "" || expectedName == "" || !strings.HasSuffix(expectedName, "_test") {
		t.Fatal("safe TEST_DATABASE_URL and TEST_POSTGRES_DB ending in _test are required")
	}
	pool, err := pgxpool.New(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("connect integration database: %v", err)
	}
	t.Cleanup(pool.Close)
	var actual string
	if err = pool.QueryRow(t.Context(), "select current_database()").Scan(&actual); err != nil || actual != expectedName {
		t.Fatalf("integration database = %q, want %q, err=%v", actual, expectedName, err)
	}
	var table *string
	if err = pool.QueryRow(t.Context(), "select to_regclass('vou_documents')::text").Scan(&table); err != nil ||
		table == nil || *table != "vou_documents" {
		t.Fatalf("VOU migrations are not applied: table=%v err=%v", table, err)
	}
	return pool
}

func truncateVOU(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		TRUNCATE vou_audit_events, vou_download_tokens, vou_document_attachments, vou_files,
			vou_expense_lines, vou_product_lines, vou_other_income_details,
			vou_expense_reimbursement_details, vou_payment_details, vou_receipt_details,
			vou_intermediary_sale_order_details, vou_purchase_order_details,
			vou_sale_order_details, vou_documents, vou_number_counters`)
	if err != nil {
		t.Fatalf("truncate VOU: %v", err)
	}
}

func createApprovedBOB(
	t *testing.T, service *bobdomain.Service, entity string, data bobdomain.CreateDetailInput,
) ReferenceInput {
	t.Helper()
	created, err := service.Create(t.Context(), entity, bobdomain.CreateInput{Data: data},
		integrationActorOne, "vou-ref-create")
	if err != nil {
		t.Fatalf("create %s reference: %v", entity, err)
	}
	submitted, err := service.Submit(t.Context(), entity, bobdomain.VersionRevisionInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
	}, integrationActorOne, "vou-ref-submit")
	if err != nil {
		t.Fatalf("submit %s reference: %v", entity, err)
	}
	approved, err := service.Approve(t.Context(), entity, bobdomain.ReviewInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: submitted.Revision,
	}, integrationActorTwo, "vou-ref-approve")
	if err != nil {
		t.Fatalf("approve %s reference: %v", entity, err)
	}
	return ReferenceInput{ObjectID: approved.ObjectID, VersionID: approved.VersionID}
}

func prepareReferences(t *testing.T, pool *pgxpool.Pool) integrationReferences {
	t.Helper()
	service := bobdomain.NewService(pool)
	suffix := newID()
	general := bobdomain.SupplierTypeGeneral
	logistics := bobdomain.SupplierTypeLogisticsPlatform
	platform := createApprovedBOB(t, service, bobdomain.EntitySupplier, bobdomain.CreateDetailInput{
		Code: "VLP" + suffix, Name: "VOU 物流平台", SupplierType: &logistics,
	})
	return integrationReferences{
		customer: createApprovedBOB(t, service, bobdomain.EntityCustomer, bobdomain.CreateDetailInput{
			Code: "VC" + suffix, Name: "VOU 客户",
		}),
		supplier: createApprovedBOB(t, service, bobdomain.EntitySupplier, bobdomain.CreateDetailInput{
			Code: "VS" + suffix, Name: "VOU 供应商", SupplierType: &general,
		}),
		employee: createApprovedBOB(t, service, bobdomain.EntityEmployee, bobdomain.CreateDetailInput{
			Code: "VE" + suffix, Name: "VOU 员工",
		}),
		product: createApprovedBOB(t, service, bobdomain.EntityProduct, bobdomain.CreateDetailInput{
			Code: "VP" + suffix, Name: "VOU 产品", Unit: "吨",
		}),
		fundAccount: createApprovedBOB(t, service, bobdomain.EntityFundAccount, bobdomain.CreateDetailInput{
			Code: "VF" + suffix, Name: "VOU 资金账户", Currency: "CNY",
		}),
		platform: platform,
		vehicle: createApprovedBOB(t, service, bobdomain.EntityVehicle, bobdomain.CreateDetailInput{
			Code: "VV" + suffix, Name: "VOU 车辆", PlateNumber: "粤V" + suffix[len(suffix)-6:],
			VehicleType: "厢式货车", PlatformObjectID: platform.ObjectID,
		}),
	}
}

func newIntegrationService(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	service, err := NewService(pool, bobdomain.NewService(pool), AttachmentOptions{Root: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new VOU service: %v", err)
	}
	return service
}

func TestVOUIntegrationAllEntitiesAndReverseLifecycle(t *testing.T) {
	pool := vouIntegrationPool(t)
	truncateVOU(t, pool)
	t.Cleanup(func() { truncateVOU(t, pool) })
	refs := prepareReferences(t, pool)
	service := newIntegrationService(t, pool)
	productLine := []ProductLineInput{{
		Product: refs.product, OrderedQuantity: "10.5", UnitPrice: "12.34",
	}}
	tests := []struct {
		entity string
		draft  DraftInput
	}{
		{EntitySaleOrder, DraftInput{
			BusinessDate: "2026-07-24", Currency: "CNY", Customer: &refs.customer, ProductLines: productLine,
		}},
		{EntityPurchaseOrder, DraftInput{
			BusinessDate: "2026-07-24", Currency: "CNY", Supplier: &refs.supplier, ProductLines: productLine,
		}},
		{EntityIntermediarySaleOrder, DraftInput{
			BusinessDate: "2026-07-24", Currency: "CNY", Customer: &refs.customer,
			Supplier: &refs.supplier, ProductLines: productLine,
		}},
		{EntityReceipt, DraftInput{
			BusinessDate: "2026-07-24", Currency: "CNY", CounterpartyType: "customer",
			Counterparty: &refs.customer, FundAccount: &refs.fundAccount, Amount: "100.00",
		}},
		{EntityPayment, DraftInput{
			BusinessDate: "2026-07-24", Currency: "CNY", CounterpartyType: "supplier",
			Counterparty: &refs.supplier, FundAccount: &refs.fundAccount, Amount: "80.00",
		}},
		{EntityExpenseReimbursement, DraftInput{
			BusinessDate: "2026-07-24", Currency: "CNY", Employee: &refs.employee, FundAccount: &refs.fundAccount,
			ExpenseLines: []ExpenseLineInput{
				{Category: "交通", Description: "出租车", Amount: "20.00"},
				{Category: "住宿", Description: "酒店", Amount: "200.00"},
			},
		}},
		{EntityOtherIncome, DraftInput{
			BusinessDate: "2026-07-24", Currency: "CNY", SourceName: "废料收入",
			CounterpartyType: "customer", Counterparty: &refs.customer,
			FundAccount: &refs.fundAccount, Amount: "60.00",
		}},
	}

	for _, test := range tests {
		t.Run(test.entity, func(t *testing.T) {
			created, err := service.Create(t.Context(), test.entity, CreateInput{Data: test.draft},
				integrationActorOne, "vou-create")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			reviewed, err := service.Review(t.Context(), test.entity, DocumentRevisionInput{
				DocumentID: created.DocumentID, Revision: created.Revision,
			}, integrationActorOne, "vou-review")
			if err != nil {
				t.Fatalf("review: %v", err)
			}
			if test.entity == EntitySaleOrder {
				if _, staleErr := service.Approve(t.Context(), test.entity, DocumentRevisionInput{
					DocumentID: created.DocumentID, Revision: created.Revision,
				}, integrationActorOne, "vou-stale-approve"); staleErr == nil {
					t.Fatal("stale revision was accepted")
				}
			}
			approved, err := service.Approve(t.Context(), test.entity, DocumentRevisionInput{
				DocumentID: created.DocumentID, Revision: reviewed.Revision,
			}, integrationActorOne, "vou-approve")
			if err != nil {
				t.Fatalf("approve: %v", err)
			}
			execute := ExecuteInput{DocumentID: created.DocumentID, Revision: approved.Revision}
			if test.entity == EntitySaleOrder || test.entity == EntityIntermediarySaleOrder {
				view, getErr := service.Get(t.Context(), test.entity, GetInput{DocumentID: created.DocumentID})
				if getErr != nil {
					t.Fatalf("get lines: %v", getErr)
				}
				execute.OutboundDate, execute.SignoffDate = "2026-07-25", "2026-07-26"
				execute.Platform, execute.Vehicle = &refs.platform, &refs.vehicle
				execute.SaleLines = []SaleExecutionLineInput{{
					LineID: view.Data.ProductLines[0].LineID, OutboundQuantity: "10",
					SignedQuantity: "8", RejectedQuantity: "1", LossQuantity: "1",
				}}
				execute.DifferenceReason = "少交 0.5"
			} else if test.entity == EntityPurchaseOrder {
				view, getErr := service.Get(t.Context(), test.entity, GetInput{DocumentID: created.DocumentID})
				if getErr != nil {
					t.Fatalf("get lines: %v", getErr)
				}
				execute.InboundDate = "2026-07-25"
				execute.PurchaseLines = []PurchaseExecutionLineInput{{
					LineID: view.Data.ProductLines[0].LineID, InboundQuantity: "10",
				}}
				execute.DifferenceReason = "少收 0.5"
			}
			executed, err := service.Execute(t.Context(), test.entity, execute,
				integrationActorOne, "vou-execute")
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			view, err := service.Get(t.Context(), test.entity, GetInput{DocumentID: created.DocumentID})
			if err != nil || view.Status != StatusExecuted || view.Amount == "" {
				t.Fatalf("executed view=%+v err=%v", view, err)
			}
			page, queryErr := service.Query(t.Context(), test.entity, QueryInput{
				Page: 1, PageSize: 20,
				Filters: QueryFilters{
					Keyword: created.DocumentNo, Status: []string{StatusExecuted},
					DateFrom: "2026-07-24", DateTo: "2026-07-24",
				},
				Sort: []SortInput{{Field: "documentNo", Order: "asc"}},
			})
			if queryErr != nil || page.Total != 1 || len(page.Items) != 1 {
				t.Fatalf("query page=%+v err=%v", page, queryErr)
			}
			if test.entity == EntitySaleOrder {
				unexecuted, reverseErr := service.Unexecute(t.Context(), test.entity, ReverseInput{
					DocumentID: created.DocumentID, Revision: executed.Revision, Reason: "修正执行结果",
				}, integrationActorOne, "vou-unexecute")
				if reverseErr != nil {
					t.Fatalf("unexecute: %v", reverseErr)
				}
				unapproved, reverseErr := service.Unapprove(t.Context(), test.entity, ReverseInput{
					DocumentID: created.DocumentID, Revision: unexecuted.Revision, Reason: "修正批准内容",
				}, integrationActorOne, "vou-unapprove")
				if reverseErr != nil {
					t.Fatalf("unapprove: %v", reverseErr)
				}
				unreviewed, reverseErr := service.Unreview(t.Context(), test.entity, ReverseInput{
					DocumentID: created.DocumentID, Revision: unapproved.Revision, Reason: "退回制单",
				}, integrationActorOne, "vou-unreview")
				if reverseErr != nil || unreviewed.Status != StatusDraft {
					t.Fatalf("unreview=%+v err=%v", unreviewed, reverseErr)
				}
				history, historyErr := service.AuditHistory(t.Context(), test.entity, HistoryInput{
					DocumentID: created.DocumentID, Page: 1, PageSize: 20,
				})
				if historyErr != nil || history.Total != 7 {
					t.Fatalf("history total=%d err=%v", history.Total, historyErr)
				}
			}
		})
	}
}

func TestVOUIntegrationAttachmentRoundTrip(t *testing.T) {
	pool := vouIntegrationPool(t)
	truncateVOU(t, pool)
	t.Cleanup(func() { truncateVOU(t, pool) })
	refs := prepareReferences(t, pool)
	service := newIntegrationService(t, pool)
	created, err := service.Create(t.Context(), EntityReceipt, CreateInput{Data: DraftInput{
		BusinessDate: "2026-07-24", Currency: "CNY", CounterpartyType: "customer",
		Counterparty: &refs.customer, FundAccount: &refs.fundAccount, Amount: "10.00",
	}}, integrationActorOne, "attachment-create")
	if err != nil {
		t.Fatalf("create receipt: %v", err)
	}
	content := []byte("%PDF-1.7\nintegration")
	sum := sha256.Sum256(content)
	initiated, err := service.InitiateAttachment(t.Context(), EntityReceipt, AttachmentInitiateInput{
		DocumentID: created.DocumentID, Revision: created.Revision, FileName: "invoice.pdf",
		ContentType: "application/pdf", Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:]),
	}, integrationActorOne, "attachment-initiate")
	if err != nil {
		t.Fatalf("initiate attachment: %v", err)
	}
	token := strings.TrimPrefix(initiated.UploadURL, "/files/attachments/upload/")
	if token == "" {
		t.Fatal("upload token is empty")
	}
	router := newVOUTestRouter(service, authorization.Func(
		func(context.Context, *http.Request, string, string) (authorization.Principal, error) {
			return authorization.Principal{ActorID: integrationActorOne}, nil
		},
	))
	uploadRequest := httptest.NewRequest(http.MethodPut, initiated.UploadURL, bytes.NewReader(content))
	uploadRequest.Header.Set("Content-Type", "application/pdf")
	uploadRecorder := httptest.NewRecorder()
	router.ServeHTTP(uploadRecorder, uploadRequest)
	if uploadRecorder.Code != http.StatusNoContent {
		t.Fatalf("upload status=%d body=%s", uploadRecorder.Code, uploadRecorder.Body.String())
	}
	replayRequest := httptest.NewRequest(http.MethodPut, initiated.UploadURL, bytes.NewReader(content))
	replayRequest.Header.Set("Content-Type", "application/pdf")
	replayRecorder := httptest.NewRecorder()
	router.ServeHTTP(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusBadRequest {
		t.Fatalf("replay status=%d", replayRecorder.Code)
	}
	download, err := service.CreateDownload(t.Context(), EntityReceipt, AttachmentDownloadInput{
		DocumentID: created.DocumentID, FileID: initiated.FileID,
	}, integrationActorOne)
	if err != nil {
		t.Fatalf("create download: %v", err)
	}
	downloadRequest := httptest.NewRequest(http.MethodGet, download.DownloadURL, nil)
	downloadRecorder := httptest.NewRecorder()
	router.ServeHTTP(downloadRecorder, downloadRequest)
	if downloadRecorder.Code != http.StatusOK || !bytes.Equal(downloadRecorder.Body.Bytes(), content) {
		t.Fatalf("download status=%d body=%q", downloadRecorder.Code, downloadRecorder.Body.Bytes())
	}
	if downloadRecorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download security headers = %#v", downloadRecorder.Header())
	}
	if _, err = service.RemoveAttachment(t.Context(), EntityReceipt, AttachmentRemoveInput{
		DocumentID: created.DocumentID, Revision: initiated.Revision, FileID: initiated.FileID,
	}, integrationActorOne, "attachment-remove"); err != nil {
		t.Fatalf("remove attachment: %v", err)
	}
}

func TestVOUIntegrationConcurrentNumberingAndPermissions(t *testing.T) {
	pool := vouIntegrationPool(t)
	truncateVOU(t, pool)
	t.Cleanup(func() { truncateVOU(t, pool) })
	refs := prepareReferences(t, pool)
	service := newIntegrationService(t, pool)
	const count = 8
	numbers := make(chan string, count)
	errorsChannel := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.Create(context.Background(), EntityReceipt, CreateInput{Data: DraftInput{
				BusinessDate: "2026-07-24", Currency: "CNY", CounterpartyType: "customer",
				Counterparty: &refs.customer, FundAccount: &refs.fundAccount, Amount: "1.00",
			}}, integrationActorOne, "concurrent-number")
			if err != nil {
				errorsChannel <- err
				return
			}
			numbers <- result.DocumentNo
		}()
	}
	group.Wait()
	close(numbers)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatalf("concurrent create: %v", err)
	}
	seen := map[string]bool{}
	for number := range numbers {
		if seen[number] {
			t.Fatalf("duplicate document number %s", number)
		}
		seen[number] = true
	}
	if len(seen) != count {
		t.Fatalf("numbers = %d, want %d", len(seen), count)
	}
	var permissionCount int
	if err := pool.QueryRow(t.Context(), "select count(*) from app_permissions where domain = 'vou'").Scan(&permissionCount); err != nil {
		t.Fatalf("count VOU permissions: %v", err)
	}
	if permissionCount != len(entities)*len(actionRoutes) {
		t.Fatalf("VOU permissions = %d, want %d", permissionCount, len(entities)*len(actionRoutes))
	}
}

func TestVOUIntegrationRejectsInvalidReferencesAndDatabaseContracts(t *testing.T) {
	pool := vouIntegrationPool(t)
	truncateVOU(t, pool)
	t.Cleanup(func() { truncateVOU(t, pool) })
	refs := prepareReferences(t, pool)
	service := newIntegrationService(t, pool)

	_, err := service.Create(t.Context(), EntityPurchaseOrder, CreateInput{Data: DraftInput{
		BusinessDate: "2026-07-24", Currency: "CNY", Supplier: &refs.platform,
		ProductLines: []ProductLineInput{{
			Product: refs.product, OrderedQuantity: "1", UnitPrice: "1.00",
		}},
	}}, integrationActorOne, "logistics-as-supplier")
	if err == nil {
		t.Fatal("purchase accepted logistics platform as supplier")
	}

	usdAccount := createApprovedBOB(t, bobdomain.NewService(pool), bobdomain.EntityFundAccount,
		bobdomain.CreateDetailInput{Code: "USD" + newID(), Name: "美元账户", Currency: "USD"})
	_, err = service.Create(t.Context(), EntityReceipt, CreateInput{Data: DraftInput{
		BusinessDate: "2026-07-24", Currency: "CNY", CounterpartyType: "customer",
		Counterparty: &refs.customer, FundAccount: &usdAccount, Amount: "1.00",
	}}, integrationActorOne, "currency-mismatch")
	if err == nil {
		t.Fatal("receipt accepted mismatched fund account currency")
	}

	created, err := service.Create(t.Context(), EntitySaleOrder, CreateInput{Data: DraftInput{
		BusinessDate: "2026-07-24", Currency: "CNY", Customer: &refs.customer,
		ProductLines: []ProductLineInput{{
			Product: refs.product, OrderedQuantity: "1", UnitPrice: "1.00",
		}},
	}}, integrationActorOne, "platform-mismatch-create")
	if err != nil {
		t.Fatalf("create sale: %v", err)
	}
	reviewed, _ := service.Review(t.Context(), EntitySaleOrder, DocumentRevisionInput{
		DocumentID: created.DocumentID, Revision: created.Revision,
	}, integrationActorOne, "platform-mismatch-review")
	approved, _ := service.Approve(t.Context(), EntitySaleOrder, DocumentRevisionInput{
		DocumentID: created.DocumentID, Revision: reviewed.Revision,
	}, integrationActorOne, "platform-mismatch-approve")
	view, _ := service.Get(t.Context(), EntitySaleOrder, GetInput{DocumentID: created.DocumentID})
	logistics := bobdomain.SupplierTypeLogisticsPlatform
	otherPlatform := createApprovedBOB(t, bobdomain.NewService(pool), bobdomain.EntitySupplier,
		bobdomain.CreateDetailInput{Code: "OLP" + newID(), Name: "其它物流", SupplierType: &logistics})
	_, err = service.Execute(t.Context(), EntitySaleOrder, ExecuteInput{
		DocumentID: created.DocumentID, Revision: approved.Revision,
		OutboundDate: "2026-07-24", SignoffDate: "2026-07-24",
		Platform: &otherPlatform, Vehicle: &refs.vehicle,
		SaleLines: []SaleExecutionLineInput{{
			LineID: view.Data.ProductLines[0].LineID, OutboundQuantity: "1",
			SignedQuantity: "1", RejectedQuantity: "0", LossQuantity: "0",
		}},
	}, integrationActorOne, "platform-mismatch-execute")
	if err == nil {
		t.Fatal("sale accepted vehicle from another platform")
	}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin invalid document: %v", err)
	}
	_, err = tx.Exec(t.Context(), `
		INSERT INTO vou_documents (
			id, entity, document_no, business_date, currency, total_amount_cents, created_by, updated_by
		) VALUES ($1, 'receipt', $2, DATE '2026-07-24', 'CNY', 100, $3, $3)`,
		newID(), "REC-20260724-999999", integrationActorOne)
	if err != nil {
		t.Fatalf("insert invalid document: %v", err)
	}
	if err = tx.Commit(t.Context()); err == nil {
		t.Fatal("database accepted document without typed detail")
	}
}
