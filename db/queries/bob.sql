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
INSERT INTO bob_supplier_versions (version_id, name) VALUES (sqlc.arg(version_id), sqlc.arg(name));

-- name: InsertBobEmployeeDetail :exec
INSERT INTO bob_employee_versions (version_id, name) VALUES (sqlc.arg(version_id), sqlc.arg(name));

-- name: InsertBobProductDetail :exec
INSERT INTO bob_product_versions (version_id, name, unit) VALUES (sqlc.arg(version_id), sqlc.arg(name), sqlc.arg(unit));

-- name: InsertBobServiceDetail :exec
INSERT INTO bob_service_versions (version_id, name, unit) VALUES (sqlc.arg(version_id), sqlc.arg(name), sqlc.arg(unit));

-- name: InsertBobFundAccountDetail :exec
INSERT INTO bob_fund_account_versions (version_id, name, currency) VALUES (sqlc.arg(version_id), sqlc.arg(name), sqlc.arg(currency));

-- name: CopyBobCustomerDetail :exec
INSERT INTO bob_customer_versions (version_id, name)
SELECT sqlc.arg(new_version_id), d.name FROM bob_customer_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobSupplierDetail :exec
INSERT INTO bob_supplier_versions (version_id, name)
SELECT sqlc.arg(new_version_id), d.name FROM bob_supplier_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobEmployeeDetail :exec
INSERT INTO bob_employee_versions (version_id, name)
SELECT sqlc.arg(new_version_id), d.name FROM bob_employee_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobProductDetail :exec
INSERT INTO bob_product_versions (version_id, name, unit)
SELECT sqlc.arg(new_version_id), d.name, d.unit FROM bob_product_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobServiceDetail :exec
INSERT INTO bob_service_versions (version_id, name, unit)
SELECT sqlc.arg(new_version_id), d.name, d.unit FROM bob_service_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: CopyBobFundAccountDetail :exec
INSERT INTO bob_fund_account_versions (version_id, name, currency)
SELECT sqlc.arg(new_version_id), d.name, d.currency FROM bob_fund_account_versions d WHERE d.version_id = sqlc.arg(source_version_id);

-- name: UpdateBobCustomerDetail :execrows
UPDATE bob_customer_versions SET name = sqlc.arg(name) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobSupplierDetail :execrows
UPDATE bob_supplier_versions SET name = sqlc.arg(name) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobEmployeeDetail :execrows
UPDATE bob_employee_versions SET name = sqlc.arg(name) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobProductDetail :execrows
UPDATE bob_product_versions SET name = sqlc.arg(name), unit = sqlc.arg(unit) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobServiceDetail :execrows
UPDATE bob_service_versions SET name = sqlc.arg(name), unit = sqlc.arg(unit) WHERE version_id = sqlc.arg(version_id);

-- name: UpdateBobFundAccountDetail :execrows
UPDATE bob_fund_account_versions SET name = sqlc.arg(name), currency = sqlc.arg(currency) WHERE version_id = sqlc.arg(version_id);

-- name: LockBobObject :one
SELECT id, entity, code, current_version_id, effective_version_id, next_version_no, revision, updated_at
FROM bob_objects
WHERE id = sqlc.arg(id) AND entity = sqlc.arg(entity)
FOR UPDATE;

-- name: LockBobVersion :one
SELECT id, object_id, entity, version_no, status, revision, submitted_by
FROM bob_versions
WHERE id = sqlc.arg(id) AND object_id = sqlc.arg(object_id) AND entity = sqlc.arg(entity)
FOR UPDATE;

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
  AND (sqlc.arg(keyword)::text = '' OR code ILIKE '%' || sqlc.arg(keyword) || '%' OR name ILIKE '%' || sqlc.arg(keyword) || '%');

-- name: ListBobObjects :many
SELECT *
FROM bob_version_views
WHERE entity = sqlc.arg(entity) AND version_id = current_version_id
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.arg(keyword)::text = '' OR code ILIKE '%' || sqlc.arg(keyword) || '%' OR name ILIKE '%' || sqlc.arg(keyword) || '%')
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
       COALESCE(c.name, s.name, e.name, p.name, sv.name, f.name) AS name,
       COALESCE(p.unit, sv.unit, '') AS unit, f.currency
FROM bob_objects o
JOIN bob_versions v ON v.object_id = o.id AND v.entity = o.entity
LEFT JOIN bob_customer_versions c ON c.version_id = v.id
LEFT JOIN bob_supplier_versions s ON s.version_id = v.id
LEFT JOIN bob_employee_versions e ON e.version_id = v.id
LEFT JOIN bob_product_versions p ON p.version_id = v.id
LEFT JOIN bob_service_versions sv ON sv.version_id = v.id
LEFT JOIN bob_fund_account_versions f ON f.version_id = v.id
WHERE o.id = sqlc.arg(object_id) AND o.entity = sqlc.arg(entity)
  AND v.id = sqlc.arg(version_id) AND o.effective_version_id = v.id AND v.status = 'EFFECTIVE'
FOR SHARE OF o;
