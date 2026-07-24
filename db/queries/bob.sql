-- name: InsertBobObject :exec
INSERT INTO bob_objects (
    id, entity, code, current_version_id, next_version_no, revision, created_by, updated_by
) VALUES (
    sqlc.arg(id), sqlc.arg(entity), sqlc.arg(code), sqlc.arg(current_version_id), 2, 1, sqlc.arg(actor_id), sqlc.arg(actor_id)
);

-- name: InsertBobVersion :exec
INSERT INTO bob_versions (
    id, object_id, entity, version_no, status, revision, created_by, updated_by
) VALUES (
    sqlc.arg(id), sqlc.arg(object_id), sqlc.arg(entity), sqlc.arg(version_no), 'DRAFT', 1, sqlc.arg(actor_id), sqlc.arg(actor_id)
);

-- name: InsertBobCustomerDetail :exec
INSERT INTO bob_customer_versions (version_id, name) VALUES (sqlc.arg(version_id), sqlc.arg(name));

-- name: InsertBobSupplierDetail :exec
INSERT INTO bob_supplier_versions (version_id, name, supplier_type)
VALUES (sqlc.arg(version_id), sqlc.arg(name), sqlc.arg(supplier_type));

-- name: InsertBobEmployeeDetail :exec
INSERT INTO bob_employee_versions (version_id, name) VALUES (sqlc.arg(version_id), sqlc.arg(name));

-- name: InsertBobProductDetail :exec
INSERT INTO bob_product_versions (version_id, name, unit) VALUES (sqlc.arg(version_id), sqlc.arg(name), sqlc.arg(unit));

-- name: InsertBobServiceDetail :exec
INSERT INTO bob_service_versions (version_id, name, unit) VALUES (sqlc.arg(version_id), sqlc.arg(name), sqlc.arg(unit));

-- name: InsertBobWarehouseDetail :exec
INSERT INTO bob_warehouse_versions (version_id, name) VALUES (sqlc.arg(version_id), sqlc.arg(name));

-- name: InsertBobVehicleDetail :exec
INSERT INTO bob_vehicle_versions (version_id, name, plate_number, vehicle_type, platform_object_id)
VALUES (
    sqlc.arg(version_id), sqlc.arg(name), sqlc.arg(plate_number),
    sqlc.arg(vehicle_type), sqlc.arg(platform_object_id)
);

-- name: InsertBobFundAccountDetail :exec
INSERT INTO bob_fund_account_versions (version_id, name, currency) VALUES (sqlc.arg(version_id), sqlc.arg(name), sqlc.arg(currency));

-- name: CopyBobCustomerDetail :exec
INSERT INTO bob_customer_versions (version_id, name)
SELECT sqlc.arg(new_version_id), d.name FROM bob_customer_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobSupplierDetail :exec
INSERT INTO bob_supplier_versions (version_id, name, supplier_type)
SELECT sqlc.arg(new_version_id), d.name, d.supplier_type
FROM bob_supplier_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobEmployeeDetail :exec
INSERT INTO bob_employee_versions (version_id, name)
SELECT sqlc.arg(new_version_id), d.name FROM bob_employee_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobProductDetail :exec
INSERT INTO bob_product_versions (version_id, name, unit)
SELECT sqlc.arg(new_version_id), d.name, d.unit FROM bob_product_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobServiceDetail :exec
INSERT INTO bob_service_versions (version_id, name, unit)
SELECT sqlc.arg(new_version_id), d.name, d.unit FROM bob_service_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobWarehouseDetail :exec
INSERT INTO bob_warehouse_versions (version_id, name)
SELECT sqlc.arg(new_version_id), d.name FROM bob_warehouse_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobVehicleDetail :exec
INSERT INTO bob_vehicle_versions (version_id, name, plate_number, vehicle_type, platform_object_id)
SELECT sqlc.arg(new_version_id), d.name, d.plate_number, d.vehicle_type, d.platform_object_id
FROM bob_vehicle_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobFundAccountDetail :exec
INSERT INTO bob_fund_account_versions (version_id, name, currency)
SELECT sqlc.arg(new_version_id), d.name, d.currency FROM bob_fund_account_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: UpdateBobCustomerDetail :execrows
UPDATE bob_customer_versions SET name = sqlc.arg(name) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobSupplierDetail :execrows
UPDATE bob_supplier_versions
SET name = sqlc.arg(name), supplier_type = sqlc.arg(supplier_type)
WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobEmployeeDetail :execrows
UPDATE bob_employee_versions SET name = sqlc.arg(name) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobProductDetail :execrows
UPDATE bob_product_versions SET name = sqlc.arg(name), unit = sqlc.arg(unit) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobServiceDetail :execrows
UPDATE bob_service_versions SET name = sqlc.arg(name), unit = sqlc.arg(unit) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobWarehouseDetail :execrows
UPDATE bob_warehouse_versions SET name = sqlc.arg(name) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobVehicleDetail :execrows
UPDATE bob_vehicle_versions
SET name = sqlc.arg(name), plate_number = sqlc.arg(plate_number),
    vehicle_type = sqlc.arg(vehicle_type), platform_object_id = sqlc.arg(platform_object_id)
WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobFundAccountDetail :execrows
UPDATE bob_fund_account_versions SET name = sqlc.arg(name), currency = sqlc.arg(currency) WHERE version_id = sqlc.arg(version_id);

-- name: LockBobObject :one
SELECT id, entity, code, current_version_id, effective_version_id, next_version_no, revision, updated_at
FROM bob_objects
WHERE id = sqlc.arg(id) AND entity = sqlc.arg(entity)
FOR UPDATE;

-- name: FindBobObjectIDByCode :one
SELECT id
FROM bob_objects
WHERE entity = sqlc.arg(entity) AND upper(code) = upper(sqlc.arg(code)::text)
LIMIT 1;

-- name: LockBobVersion :one
SELECT id, object_id, entity, version_no, status, revision,
       submitted_at, submitted_by, reviewed_at, reviewed_by
FROM bob_versions
WHERE id = sqlc.arg(id) AND object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
FOR UPDATE;

-- name: BobDraftAuditIsDeletable :one
SELECT count(*) >= 1
   AND count(*) FILTER (WHERE event_type = 'CREATED') = 1
   AND bool_and(event_type IN ('CREATED', 'SAVED'))
FROM bob_audit_events
WHERE object_id = sqlc.arg(object_id)
  AND version_id = sqlc.arg(version_id)
  AND entity = sqlc.arg(entity);

-- name: BobObjectHasExternalReferences :one
SELECT EXISTS (
    SELECT 1
    FROM bob_vehicle_versions vehicle
    WHERE vehicle.platform_object_id = sqlc.arg(target_object_id)

    UNION ALL

    SELECT 1
    FROM vou_sale_order_details sale_order
    WHERE sale_order.customer_object_id = sqlc.arg(target_object_id)
       OR sale_order.customer_version_id = sqlc.arg(target_version_id)
       OR sale_order.platform_object_id = sqlc.arg(target_object_id)
       OR sale_order.platform_version_id = sqlc.arg(target_version_id)
       OR sale_order.vehicle_object_id = sqlc.arg(target_object_id)
       OR sale_order.vehicle_version_id = sqlc.arg(target_version_id)

    UNION ALL

    SELECT 1
    FROM vou_purchase_order_details purchase_order
    WHERE purchase_order.supplier_object_id = sqlc.arg(target_object_id)
       OR purchase_order.supplier_version_id = sqlc.arg(target_version_id)

    UNION ALL

    SELECT 1
    FROM vou_intermediary_sale_order_details intermediary
    WHERE intermediary.customer_object_id = sqlc.arg(target_object_id)
       OR intermediary.customer_version_id = sqlc.arg(target_version_id)
       OR intermediary.supplier_object_id = sqlc.arg(target_object_id)
       OR intermediary.supplier_version_id = sqlc.arg(target_version_id)
       OR intermediary.platform_object_id = sqlc.arg(target_object_id)
       OR intermediary.platform_version_id = sqlc.arg(target_version_id)
       OR intermediary.vehicle_object_id = sqlc.arg(target_object_id)
       OR intermediary.vehicle_version_id = sqlc.arg(target_version_id)

    UNION ALL

    SELECT 1
    FROM vou_receipt_details receipt
    WHERE receipt.counterparty_object_id = sqlc.arg(target_object_id)
       OR receipt.counterparty_version_id = sqlc.arg(target_version_id)
       OR receipt.fund_account_object_id = sqlc.arg(target_object_id)
       OR receipt.fund_account_version_id = sqlc.arg(target_version_id)

    UNION ALL

    SELECT 1
    FROM vou_payment_details payment
    WHERE payment.counterparty_object_id = sqlc.arg(target_object_id)
       OR payment.counterparty_version_id = sqlc.arg(target_version_id)
       OR payment.fund_account_object_id = sqlc.arg(target_object_id)
       OR payment.fund_account_version_id = sqlc.arg(target_version_id)

    UNION ALL

    SELECT 1
    FROM vou_expense_reimbursement_details reimbursement
    WHERE reimbursement.employee_object_id = sqlc.arg(target_object_id)
       OR reimbursement.employee_version_id = sqlc.arg(target_version_id)
       OR reimbursement.fund_account_object_id = sqlc.arg(target_object_id)
       OR reimbursement.fund_account_version_id = sqlc.arg(target_version_id)

    UNION ALL

    SELECT 1
    FROM vou_other_income_details other_income
    WHERE other_income.counterparty_object_id = sqlc.arg(target_object_id)
       OR other_income.counterparty_version_id = sqlc.arg(target_version_id)
       OR other_income.fund_account_object_id = sqlc.arg(target_object_id)
       OR other_income.fund_account_version_id = sqlc.arg(target_version_id)

    UNION ALL

    SELECT 1
    FROM vou_product_lines product_line
    WHERE product_line.product_object_id = sqlc.arg(target_object_id)
       OR product_line.product_version_id = sqlc.arg(target_version_id)
);

-- name: DeleteBobAuditEventsForDraft :execrows
DELETE FROM bob_audit_events
WHERE object_id = sqlc.arg(object_id)
  AND version_id = sqlc.arg(version_id)
  AND entity = sqlc.arg(entity)
  AND event_type IN ('CREATED', 'SAVED');

-- name: DeleteBobCustomerDetail :execrows
DELETE FROM bob_customer_versions WHERE version_id = sqlc.arg(version_id);

-- name: DeleteBobSupplierDetail :execrows
DELETE FROM bob_supplier_versions WHERE version_id = sqlc.arg(version_id);

-- name: DeleteBobEmployeeDetail :execrows
DELETE FROM bob_employee_versions WHERE version_id = sqlc.arg(version_id);

-- name: DeleteBobProductDetail :execrows
DELETE FROM bob_product_versions WHERE version_id = sqlc.arg(version_id);

-- name: DeleteBobServiceDetail :execrows
DELETE FROM bob_service_versions WHERE version_id = sqlc.arg(version_id);

-- name: DeleteBobWarehouseDetail :execrows
DELETE FROM bob_warehouse_versions WHERE version_id = sqlc.arg(version_id);

-- name: DeleteBobVehicleDetail :execrows
DELETE FROM bob_vehicle_versions WHERE version_id = sqlc.arg(version_id);

-- name: DeleteBobFundAccountDetail :execrows
DELETE FROM bob_fund_account_versions WHERE version_id = sqlc.arg(version_id);

-- name: DeleteBobFirstVersion :execrows
DELETE FROM bob_versions
WHERE id = sqlc.arg(version_id)
  AND object_id = sqlc.arg(object_id)
  AND entity = sqlc.arg(entity)
  AND version_no = 1
  AND status = 'DRAFT'
  AND revision = sqlc.arg(revision)
  AND submitted_at IS NULL
  AND submitted_by IS NULL
  AND reviewed_at IS NULL
  AND reviewed_by IS NULL;

-- name: DeleteBobObject :execrows
DELETE FROM bob_objects
WHERE id = sqlc.arg(object_id)
  AND entity = sqlc.arg(entity)
  AND current_version_id = sqlc.arg(version_id)
  AND effective_version_id IS NULL
  AND next_version_no = 2
  AND revision = sqlc.arg(object_revision);

-- name: LockEffectiveLogisticsPlatform :one
SELECT o.id
FROM bob_objects o
JOIN bob_versions v
  ON v.id = o.effective_version_id
 AND v.object_id = o.id
 AND v.entity = o.entity
JOIN bob_supplier_versions s ON s.version_id = v.id
WHERE o.id = sqlc.arg(platform_object_id)
  AND o.entity = 'supplier'
  AND o.current_version_id = o.effective_version_id
  AND v.status = 'EFFECTIVE'
  AND s.supplier_type = 'LOGISTICS_PLATFORM'
FOR SHARE OF o;

-- name: MarkBobVersionSaved :execrows
UPDATE bob_versions
SET revision = revision + 1, updated_at = now(), updated_by = sqlc.arg(actor_id)
WHERE id = sqlc.arg(id) AND object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
  AND revision = sqlc.arg(revision) AND status IN ('DRAFT', 'REJECTED');

-- name: SubmitBobVersion :execrows
UPDATE bob_versions
SET status = 'PENDING', revision = revision + 1, submitted_at = now(), submitted_by = sqlc.arg(actor_id),
    reviewed_at = NULL, reviewed_by = NULL, review_comment = NULL, updated_at = now(), updated_by = sqlc.arg(actor_id)
WHERE id = sqlc.arg(id) AND object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
  AND revision = sqlc.arg(revision) AND status IN ('DRAFT', 'REJECTED');

-- name: ApproveBobVersion :execrows
UPDATE bob_versions
SET status = 'EFFECTIVE', revision = revision + 1, reviewed_at = now(), reviewed_by = sqlc.arg(actor_id),
    review_comment = sqlc.narg(comment), updated_at = now(), updated_by = sqlc.arg(actor_id)
WHERE id = sqlc.arg(id) AND object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
  AND revision = sqlc.arg(revision) AND status = 'PENDING' AND submitted_by <> sqlc.arg(actor_id);

-- name: RejectBobVersion :execrows
UPDATE bob_versions
SET status = 'REJECTED', revision = revision + 1, reviewed_at = now(), reviewed_by = sqlc.arg(actor_id),
    review_comment = sqlc.arg(comment), updated_at = now(), updated_by = sqlc.arg(actor_id)
WHERE id = sqlc.arg(id) AND object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
  AND revision = sqlc.arg(revision) AND status = 'PENDING' AND submitted_by <> sqlc.arg(actor_id);

-- name: InvalidateBobVersion :execrows
UPDATE bob_versions
SET status = 'INVALID', revision = revision + 1, updated_at = now(), updated_by = sqlc.arg(actor_id)
WHERE id = sqlc.arg(id) AND object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
  AND revision = sqlc.arg(revision) AND status = 'EFFECTIVE';

-- name: AdvanceBobObjectForEdit :execrows
UPDATE bob_objects
SET current_version_id = sqlc.arg(new_version_id), effective_version_id = NULL,
    next_version_no = next_version_no + 1, revision = revision + 1, updated_at = now(), updated_by = sqlc.arg(actor_id)
WHERE id = sqlc.arg(id) AND entity = sqlc.arg(entity) AND revision = sqlc.arg(revision)
  AND current_version_id = sqlc.arg(old_version_id) AND effective_version_id = sqlc.arg(old_version_id);

-- name: SetBobObjectEffective :execrows
UPDATE bob_objects
SET effective_version_id = sqlc.arg(version_id), revision = revision + 1, updated_at = now(), updated_by = sqlc.arg(actor_id)
WHERE id = sqlc.arg(id) AND entity = sqlc.arg(entity) AND current_version_id = sqlc.arg(version_id)
  AND effective_version_id IS NULL AND revision = sqlc.arg(revision);

-- name: TouchBobObject :exec
UPDATE bob_objects SET updated_at = now(), updated_by = sqlc.arg(actor_id)
WHERE id = sqlc.arg(id) AND entity = sqlc.arg(entity);

-- name: InsertBobAuditEvent :exec
INSERT INTO bob_audit_events (
    id, object_id, version_id, entity, event_type, from_status, to_status, actor_id, comment, request_id, summary
) VALUES (
    sqlc.arg(id), sqlc.arg(object_id), sqlc.arg(version_id), sqlc.arg(entity), sqlc.arg(event_type),
    sqlc.narg(from_status), sqlc.arg(to_status), sqlc.arg(actor_id), sqlc.narg(comment), sqlc.arg(request_id), sqlc.arg(summary)
);

-- name: GetBobVersionView :one
SELECT * FROM bob_version_views
WHERE object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
  AND version_id = COALESCE(NULLIF(sqlc.arg(version_id)::text, ''), current_version_id);

-- name: CountBobObjects :one
SELECT count(*)
FROM bob_version_views
WHERE entity = sqlc.arg(entity) AND version_id = current_version_id
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR status = ANY(sqlc.arg(statuses)::text[]))
  AND (
      sqlc.arg(keyword)::text = ''
      OR code ILIKE '%' || sqlc.arg(keyword) || '%'
      OR name ILIKE '%' || sqlc.arg(keyword) || '%'
      OR (entity = 'vehicle' AND plate_number ILIKE '%' || sqlc.arg(keyword) || '%')
  );

-- name: ListBobObjects :many
SELECT *
FROM bob_version_views
WHERE entity = sqlc.arg(entity) AND version_id = current_version_id
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR status = ANY(sqlc.arg(statuses)::text[]))
  AND (
      sqlc.arg(keyword)::text = ''
      OR code ILIKE '%' || sqlc.arg(keyword) || '%'
      OR name ILIKE '%' || sqlc.arg(keyword) || '%'
      OR (entity = 'vehicle' AND plate_number ILIKE '%' || sqlc.arg(keyword) || '%')
  )
ORDER BY
  CASE WHEN sqlc.arg(sort_field)::text = 'updatedAt' AND sqlc.arg(sort_order)::text = 'asc' THEN object_updated_at END ASC,
  CASE WHEN sqlc.arg(sort_field)::text = 'updatedAt' AND sqlc.arg(sort_order)::text = 'desc' THEN object_updated_at END DESC,
  CASE WHEN sqlc.arg(sort_field)::text = 'code' AND sqlc.arg(sort_order)::text = 'asc' THEN code END ASC,
  CASE WHEN sqlc.arg(sort_field)::text = 'code' AND sqlc.arg(sort_order)::text = 'desc' THEN code END DESC,
  CASE WHEN sqlc.arg(sort_field)::text = 'name' AND sqlc.arg(sort_order)::text = 'asc' THEN name END ASC,
  CASE WHEN sqlc.arg(sort_field)::text = 'name' AND sqlc.arg(sort_order)::text = 'desc' THEN name END DESC,
  CASE WHEN sqlc.arg(sort_field)::text = 'status' AND sqlc.arg(sort_order)::text = 'asc' THEN status END ASC,
  CASE WHEN sqlc.arg(sort_field)::text = 'status' AND sqlc.arg(sort_order)::text = 'desc' THEN status END DESC,
  CASE WHEN sqlc.arg(sort_field)::text = 'version' AND sqlc.arg(sort_order)::text = 'asc' THEN version_no END ASC,
  CASE WHEN sqlc.arg(sort_field)::text = 'version' AND sqlc.arg(sort_order)::text = 'desc' THEN version_no END DESC,
  object_id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountBobVersions :one
SELECT count(*) FROM bob_versions WHERE object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity);

-- name: ListBobVersions :many
SELECT * FROM bob_version_views
WHERE object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
ORDER BY version_no DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountBobAuditEvents :one
SELECT count(*) FROM bob_audit_events WHERE object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity);

-- name: ListBobAuditEvents :many
SELECT id, object_id, version_id, entity, event_type, from_status, to_status, actor_id,
       occurred_at, comment, request_id, summary
FROM bob_audit_events
WHERE object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: ResolveBobEffectiveReference :one
SELECT o.id AS object_id, o.entity, o.code, v.id AS version_id,
       COALESCE(c.name, s.name, e.name, p.name, sv.name, w.name, vh.name, f.name) AS name,
       COALESCE(p.unit, sv.unit, '') AS unit, f.currency, s.supplier_type,
       vh.plate_number, vh.vehicle_type, vh.platform_object_id
FROM bob_objects o
JOIN bob_versions v ON v.object_id = o.id AND v.entity = o.entity
LEFT JOIN bob_customer_versions c ON c.version_id = v.id
LEFT JOIN bob_supplier_versions s ON s.version_id = v.id
LEFT JOIN bob_employee_versions e ON e.version_id = v.id
LEFT JOIN bob_product_versions p ON p.version_id = v.id
LEFT JOIN bob_service_versions sv ON sv.version_id = v.id
LEFT JOIN bob_warehouse_versions w ON w.version_id = v.id
LEFT JOIN bob_vehicle_versions vh ON vh.version_id = v.id
LEFT JOIN bob_fund_account_versions f ON f.version_id = v.id
WHERE o.id = sqlc.arg(object_id) AND o.entity = sqlc.arg(entity)
  AND v.id = sqlc.arg(version_id) AND o.effective_version_id = v.id AND v.status = 'EFFECTIVE'
FOR SHARE OF o;
