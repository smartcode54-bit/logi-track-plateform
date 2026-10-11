-- 0012_customer_scope_party_read.sql: Appendix A §A.2.10 (issue T20). Never edit this file once it is merged;
-- follow-ups take the next number (R31).
-- A customer-scope principal (app.role = 'customer', its billing_parties ids in app.customer_ids) reads only its own
-- billing parties and their customers (T20 acceptance: "a customer-scoped user sees only its own billing party";
-- Appendix B §B.1.4: a customer-scoped principal sees only rows linked to its parties). 0003 gave customers,
-- billing_parties and customer_driver_id_types the global-read policy p_read = app_is_authenticated() of
-- app_rls_global_table (R60); this narrows only the customer role. Staff, drivers, dispatchers (their app.role is
-- their membership role) and stewards read every row as before; p_steward_write and p_bypass are unchanged, and the
-- policy names stay those of Appendix C §C.3.0 (internal/platform/db/rls_catalog_test.go).
-- Depends on: 0001 (app_is_authenticated, app_role, app_customer_ids), 0003 (the three tables and their p_read).
-- No GRANT/REVOKE and no new table (R66). Production order: P1 applies it with `migrate up` (T24 runbook) after the
-- held 0010 or with it; it depends on nothing in 0010 or 0011.

-- +goose Up
ALTER POLICY p_read ON billing_parties
  USING (app_is_authenticated() AND (app_role() IS DISTINCT FROM 'customer' OR id = ANY (app_customer_ids())));

-- The EXISTS runs under billing_parties' own RLS (the narrowed p_read above), so it can only find a party of the
-- caller's scope.
ALTER POLICY p_read ON customers
  USING (app_is_authenticated() AND (app_role() IS DISTINCT FROM 'customer'
         OR EXISTS (SELECT 1 FROM billing_parties bp
                     WHERE bp.customer_id = customers.id AND bp.id = ANY (app_customer_ids()))));

ALTER POLICY p_read ON customer_driver_id_types
  USING (app_is_authenticated() AND (app_role() IS DISTINCT FROM 'customer'
         OR EXISTS (SELECT 1 FROM billing_parties bp
                     WHERE bp.customer_id = customer_driver_id_types.customer_id AND bp.id = ANY (app_customer_ids()))));

-- +goose Down
-- The 0003 expressions of app_rls_global_table.
ALTER POLICY p_read ON customer_driver_id_types USING (app_is_authenticated());
ALTER POLICY p_read ON customers USING (app_is_authenticated());
ALTER POLICY p_read ON billing_parties USING (app_is_authenticated());
