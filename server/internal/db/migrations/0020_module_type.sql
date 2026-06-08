-- Classify each module as either an Access Control Unit (ACU) that makes
-- door-unlock decisions, or a Provisioning & Enrollment Unit (PEU) that
-- drives the two-scan enrolment flow. Existing rows are ACUs.
ALTER TABLE modules
  ADD COLUMN module_type TEXT NOT NULL DEFAULT 'access_control_unit'
  CHECK (module_type IN ('access_control_unit', 'provisioning_enrollment_unit'));
